package webrtc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	pion "github.com/pion/webrtc/v4"
)

const (
	maxControlMessageBytes = 8 * 1024
	controlQueueCapacity   = 32
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

// PionControlReceiver decouples Pion callbacks from durable mode transitions. One bounded worker
// preserves channel order and prevents a slow outbox from blocking SCTP packet processing.
type PionControlReceiver struct {
	channel      pionControlDataChannel
	handler      ControlCommandHandler
	sessionID    string
	connectionID string

	ctx    context.Context
	cancel context.CancelFunc
	queue  chan controlMessage
	done   chan struct{}
	sendMu sync.Mutex
}

func newPionControlReceiver(
	channel pionControlDataChannel,
	handler ControlCommandHandler,
	sessionID string,
	connectionID string,
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
		ctx: ctx, cancel: cancel, queue: make(chan controlMessage, controlQueueCapacity), done: make(chan struct{}),
	}
	channel.OnMessage(receiver.onMessage)
	channel.OnClose(cancel)
	channel.OnError(func(error) { cancel() })
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
		_ = r.send(protocolError(requestIDFromJSON(item.data), realtimev1.ErrorControlUnavailable))
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
			if err := r.send(response); err != nil {
				r.cancel()
				return
			}
		}
	}
}

func (r *PionControlReceiver) handle(message controlMessage) realtimev1.ControlResponse {
	if !message.isString || len(message.data) == 0 || len(message.data) > maxControlMessageBytes {
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
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	return sendControlResponse(r.channel, response)
}

// Close cancels in-flight mode work before closing the SCTP channel and waits for the ordered
// worker when the caller's shutdown deadline permits.
func (r *PionControlReceiver) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.cancel()
	closeErr := r.channel.Close()
	select {
	case <-r.done:
		return closeErr
	case <-ctx.Done():
		return errors.Join(closeErr, ctx.Err())
	}
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
	return realtimev1.ControlResponse{
		ProtocolVersion: realtimev1.ControlProtocolVersion,
		Type:            realtimev1.ControlMessageError,
		RequestID:       requestID,
		Error: &realtimev1.ControlError{
			Code:    code,
			Message: string(code),
		},
	}
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
