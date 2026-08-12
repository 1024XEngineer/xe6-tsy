package webrtc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	pion "github.com/pion/webrtc/v4"
)

const (
	maxControlMessageBytes  = 8 * 1024
	controlQueueCapacity    = 32
	controlResponseCapacity = controlQueueCapacity * 2
)

var (
	ErrControlHandlerRequired = errors.New("WebRTC control handler is required")
	ErrControlChannelInvalid  = errors.New("WebRTC control DataChannel must be reliable and ordered")
)

// ControlCommandHandler is the typed application boundary for commands received from one
// ticket-authorized PeerConnection. Session identity is transport-bound and never read from JSON.
type ControlCommandHandler interface {
	HandleModeSwitch(
		context.Context,
		string,
		string,
		string,
		realtimev1.ControlModeSwitchCommand,
	) realtimev1.ControlResponse
}

// ControlConfig enables the optional uplink command channel without changing legacy signaling.
type ControlConfig struct {
	Handler ControlCommandHandler
}

type controlMessage struct {
	data     []byte
	isString bool
}

// pionControlDataChannel is kept separate from the downlink event channel so existing fakes and
// the translation-events protocol do not acquire uplink responsibilities.
type pionControlDataChannel interface {
	Label() string
	Ordered() bool
	MaxPacketLifeTime() *uint16
	MaxRetransmits() *uint16
	ReadyState() pion.DataChannelState
	OnMessage(func(pion.DataChannelMessage))
	OnClose(func())
	OnError(func(error))
	SendText(string) error
	Close() error
}

// pionControlPeerConnection exposes only remote-created DataChannels.
type pionControlPeerConnection interface {
	OnDataChannel(func(pionControlDataChannel))
}

// PionControlReceiver decouples Pion callbacks from durable mode transitions. Bounded command and
// response workers keep slow mode changes and SCTP writes outside the Pion receive callback.
type PionControlReceiver struct {
	channel      pionControlDataChannel
	handler      ControlCommandHandler
	sessionID    string
	connectionID string

	ctx       context.Context
	cancel    context.CancelFunc
	queue     chan controlMessage
	responses chan realtimev1.ControlResponse
	done      chan struct{}
	sendDone  chan struct{}
	onStopped func(*PionControlReceiver)
}

func newPionControlReceiver(
	channel pionControlDataChannel,
	handler ControlCommandHandler,
	sessionID string,
	connectionID string,
	onStopped func(*PionControlReceiver),
) (*PionControlReceiver, error) {
	if channel == nil || handler == nil || sessionID == "" || connectionID == "" {
		return nil, ErrControlHandlerRequired
	}
	if !channel.Ordered() || channel.MaxPacketLifeTime() != nil || channel.MaxRetransmits() != nil {
		_ = sendControlResponse(channel, protocolError("", realtimev1.ErrorControlInvalidMessage))
		_ = channel.Close()
		return nil, ErrControlChannelInvalid
	}
	ctx, cancel := context.WithCancel(context.Background())
	receiver := &PionControlReceiver{
		channel: channel, handler: handler, sessionID: sessionID, connectionID: connectionID,
		ctx: ctx, cancel: cancel, onStopped: onStopped,
		queue:     make(chan controlMessage, controlQueueCapacity),
		responses: make(chan realtimev1.ControlResponse, controlResponseCapacity),
		done:      make(chan struct{}), sendDone: make(chan struct{}),
	}
	channel.OnMessage(receiver.onMessage)
	channel.OnClose(receiver.stop)
	channel.OnError(func(error) {
		receiver.stop()
		_ = channel.Close()
	})
	go receiver.runSender()
	go receiver.run()
	return receiver, nil
}

func (r *PionControlReceiver) onMessage(message pion.DataChannelMessage) {
	if r == nil {
		return
	}
	item := controlMessage{isString: message.IsString}
	if len(message.Data) <= maxControlMessageBytes {
		item.data = append([]byte(nil), message.Data...)
	}
	select {
	case <-r.ctx.Done():
		return
	case r.queue <- item:
		return
	default:
		r.tryQueueResponse(protocolError(requestIDFromJSON(item.data), realtimev1.ErrorControlUnavailable))
	}
}

func (r *PionControlReceiver) run() {
	defer close(r.done)
	for {
		select {
		case <-r.ctx.Done():
			return
		case message := <-r.queue:
			response := r.handle(message)
			select {
			case <-r.ctx.Done():
				return
			case r.responses <- response:
			}
		}
	}
}

func (r *PionControlReceiver) runSender() {
	defer close(r.sendDone)
	for {
		select {
		case <-r.ctx.Done():
			return
		case response := <-r.responses:
			if err := r.send(response); err != nil {
				r.stop()
				_ = r.channel.Close()
				return
			}
		}
	}
}

// tryQueueResponse keeps the Pion receive callback non-blocking when command execution or SCTP
// output is slow. RequestID correlation lets overload errors arrive before older command results.
func (r *PionControlReceiver) tryQueueResponse(response realtimev1.ControlResponse) {
	select {
	case <-r.ctx.Done():
	case r.responses <- response:
	default:
		r.stop()
		_ = r.channel.Close()
	}
}

func (r *PionControlReceiver) stop() {
	r.cancel()
	if r.onStopped != nil {
		r.onStopped(r)
	}
}

func (r *PionControlReceiver) handle(message controlMessage) realtimev1.ControlResponse {
	if !message.isString || len(message.data) == 0 || len(message.data) > maxControlMessageBytes || !utf8.Valid(message.data) {
		return protocolError(requestIDFromJSON(message.data), realtimev1.ErrorControlInvalidMessage)
	}
	header, err := decodeControlHeader(message.data)
	if err != nil {
		return protocolError("", realtimev1.ErrorControlInvalidMessage)
	}
	if header.ProtocolVersion != realtimev1.ControlProtocolVersion {
		return protocolError(header.RequestID, realtimev1.ErrorControlUnsupportedVersion)
	}
	if header.Type != realtimev1.ControlMessageModeSwitch {
		return protocolError(header.RequestID, realtimev1.ErrorControlUnsupportedType)
	}
	request, err := decodeModeSwitchRequest(message.data)
	if err != nil || request.Validate() != nil {
		return protocolError(header.RequestID, realtimev1.ErrorControlInvalidMessage)
	}
	return r.handler.HandleModeSwitch(
		r.ctx,
		r.sessionID,
		r.connectionID,
		request.RequestID,
		request.Command,
	)
}

func (r *PionControlReceiver) send(response realtimev1.ControlResponse) error {
	if r == nil || r.channel == nil {
		return ErrMediaUnavailable
	}
	if err := response.Validate(); err != nil {
		return fmt.Errorf("validate control response: %w", err)
	}
	select {
	case <-r.ctx.Done():
		return r.ctx.Err()
	default:
	}
	return sendControlResponse(r.channel, response)
}

// Close cancels in-flight mode work before closing the SCTP channel and waits for the ordered
// worker when the caller's shutdown deadline permits.
func (r *PionControlReceiver) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.stop()
	closeErr := r.channel.Close()
	workerDone, senderDone := r.done, r.sendDone
	for workerDone != nil || senderDone != nil {
		select {
		case <-workerDone:
			workerDone = nil
		case <-senderDone:
			senderDone = nil
		case <-ctx.Done():
			return errors.Join(closeErr, ctx.Err())
		}
	}
	return closeErr
}

func decodeControlHeader(payload []byte) (struct {
	ProtocolVersion int                           `json:"protocol_version"`
	Type            realtimev1.ControlMessageType `json:"type"`
	RequestID       string                        `json:"request_id"`
}, error) {
	var header struct {
		ProtocolVersion int                           `json:"protocol_version"`
		Type            realtimev1.ControlMessageType `json:"type"`
		RequestID       string                        `json:"request_id"`
	}
	err := json.Unmarshal(payload, &header)
	return header, err
}

func decodeModeSwitchRequest(payload []byte) (realtimev1.ControlModeSwitchRequest, error) {
	var request realtimev1.ControlModeSwitchRequest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return realtimev1.ControlModeSwitchRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return realtimev1.ControlModeSwitchRequest{}, errors.New("control message contains trailing JSON")
	}
	return request, nil
}

func requestIDFromJSON(payload []byte) string {
	header, err := decodeControlHeader(payload)
	if err != nil {
		return ""
	}
	return header.RequestID
}

func protocolError(requestID string, code realtimev1.ControlPlaneErrorCode) realtimev1.ControlResponse {
	response := realtimev1.ControlResponse{
		ProtocolVersion: realtimev1.ControlProtocolVersion,
		Type:            realtimev1.ControlMessageError,
		RequestID:       requestID,
		Error: &realtimev1.ControlError{
			Code:    code,
			Message: string(code),
		},
	}
	// An unsafe correlation value must not turn a recoverable client error into a sender failure.
	if err := response.Validate(); err != nil {
		response.RequestID = ""
	}
	return response
}

func sendControlResponse(channel pionControlDataChannel, response realtimev1.ControlResponse) error {
	if channel == nil || channel.ReadyState() != pion.DataChannelStateOpen {
		return ErrTransportClosed
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode control response: %w", err)
	}
	if err := channel.SendText(string(payload)); err != nil {
		return fmt.Errorf("send control response: %w", err)
	}
	return nil
}
