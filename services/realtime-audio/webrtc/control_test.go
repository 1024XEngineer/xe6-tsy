package webrtc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	pion "github.com/pion/webrtc/v4"
)

func TestPionControlReceiverDispatchesBoundModeCommand(t *testing.T) {
	handler := &controlHandlerRecorder{}
	channel, _ := newTestControlReceiver(t, handler)

	request := validControlModeSwitchRequest()
	channel.Receive(t, request, true)
	response := channel.NextResponse(t)
	if response.Type != realtimev1.ControlMessageModeSwitchResult || response.Result == nil || response.Error != nil {
		t.Fatalf("response = %#v", response)
	}
	call := handler.OnlyCall(t)
	if call.sessionID != "session-1" || call.connectionID != "rtc-1" || call.requestID != request.RequestID || call.command != request.Command {
		t.Fatalf("handler call = %#v", call)
	}
}

func TestPionControlReceiverRejectsInvalidMessagesWithoutClosingAudioTransport(t *testing.T) {
	valid := validControlModeSwitchRequest()
	unknownField, _ := json.Marshal(map[string]any{
		"protocol_version": 1, "type": "mode.switch", "request_id": "request-1",
		"command": valid.Command, "session_id": "another-session",
	})
	validJSON, _ := json.Marshal(valid)
	oversized := make([]byte, maxControlMessageBytes+1)
	for index := range oversized {
		oversized[index] = 'x'
	}

	tests := []struct {
		name string
		data []byte
		text bool
		code realtimev1.ControlPlaneErrorCode
	}{
		{name: "binary", data: validJSON, code: realtimev1.ErrorControlInvalidMessage},
		{name: "empty", text: true, code: realtimev1.ErrorControlInvalidMessage},
		{name: "malformed", data: []byte(`{"protocol_version":`), text: true, code: realtimev1.ErrorControlInvalidMessage},
		{name: "unknown field and forged session", data: unknownField, text: true, code: realtimev1.ErrorControlInvalidMessage},
		{name: "trailing JSON", data: append(validJSON, []byte(` {}`)...), text: true, code: realtimev1.ErrorControlInvalidMessage},
		{name: "oversized", data: oversized, text: true, code: realtimev1.ErrorControlInvalidMessage},
		{name: "unsupported version", data: mutateControlRequest(t, valid, func(request *realtimev1.ControlModeSwitchRequest) { request.ProtocolVersion++ }), text: true, code: realtimev1.ErrorControlUnsupportedVersion},
		{name: "unsupported type", data: mutateControlRequest(t, valid, func(request *realtimev1.ControlModeSwitchRequest) { request.Type = "session.stop" }), text: true, code: realtimev1.ErrorControlUnsupportedType},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &controlHandlerRecorder{}
			channel, _ := newTestControlReceiver(t, handler)
			channel.ReceiveBytes(test.data, test.text)
			response := channel.NextResponse(t)
			if response.Error == nil || response.Error.Code != test.code || response.Result != nil {
				t.Fatalf("response = %#v, want error %q", response, test.code)
			}
			if handler.CallCount() != 0 {
				t.Fatalf("invalid message reached handler %d times", handler.CallCount())
			}
			if channel.ReadyState() != pion.DataChannelStateOpen {
				t.Fatal("invalid message closed the control channel")
			}
		})
	}
}

func TestPionControlReceiverInvalidRequestIDDoesNotStopWorker(t *testing.T) {
	for _, requestID := range []string{"request\ninvalid", strings.Repeat("r", 129)} {
		handler := &controlHandlerRecorder{}
		channel, _ := newTestControlReceiver(t, handler)
		invalid := validControlModeSwitchRequest()
		invalid.RequestID = requestID
		channel.Receive(t, invalid, true)

		response := channel.NextResponse(t)
		if response.RequestID != "" || response.Error == nil || response.Error.Code != realtimev1.ErrorControlInvalidMessage {
			t.Fatalf("invalid request ID response = %#v", response)
		}
		if err := response.Validate(); err != nil {
			t.Fatalf("error response Validate() error = %v", err)
		}

		valid := validControlModeSwitchRequest()
		channel.Receive(t, valid, true)
		if result := channel.NextResponse(t); result.Result == nil || result.RequestID != valid.RequestID {
			t.Fatalf("valid response after invalid request ID = %#v", result)
		}
	}
}

func TestPionControlReceiverBoundsQueue(t *testing.T) {
	handler := &controlHandlerRecorder{block: make(chan struct{}), entered: make(chan struct{}, 1)}
	channel, receiver := newTestControlReceiver(t, handler)

	first := validControlModeSwitchRequest()
	channel.Receive(t, first, true)
	select {
	case <-handler.entered:
	case <-time.After(time.Second):
		t.Fatal("first control command did not enter handler")
	}
	for index := 0; index < controlQueueCapacity; index++ {
		request := validControlModeSwitchRequest()
		request.RequestID = "queued-request"
		request.Command.OperationID = "queued-operation"
		channel.Receive(t, request, true)
	}
	overflow := validControlModeSwitchRequest()
	overflow.RequestID = "overflow-request"
	overflow.Command.OperationID = "overflow-operation"
	channel.sendBlock = make(chan struct{})
	received := make(chan struct{})
	go func() {
		channel.Receive(t, overflow, true)
		close(received)
	}()
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("queue overflow blocked the Pion message callback")
	}
	close(channel.sendBlock)
	response := channel.NextResponse(t)
	if response.RequestID != overflow.RequestID || response.Error == nil || response.Error.Code != realtimev1.ErrorControlUnavailable {
		t.Fatalf("overflow response = %#v", response)
	}
	close(handler.block)
	if err := receiver.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestPionControlReceiverCloseCancelsInFlightCommand(t *testing.T) {
	handler := &cancelAwareControlHandler{entered: make(chan struct{}), canceled: make(chan struct{})}
	channel, receiver := newTestControlReceiver(t, handler)
	channel.Receive(t, validControlModeSwitchRequest(), true)
	select {
	case <-handler.entered:
	case <-time.After(time.Second):
		t.Fatal("control command did not enter handler")
	}
	if err := receiver.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-handler.canceled:
	case <-time.After(time.Second):
		t.Fatal("Close() did not cancel in-flight command")
	}
	if channel.ReadyState() != pion.DataChannelStateClosed {
		t.Fatal("Close() left control channel open")
	}
}

func TestPionControlReceiverRejectsNonReliableOrderedChannel(t *testing.T) {
	partialReliability := uint16(1)
	partialChannel := newControlChannelRecorder(realtimev1.ControlDataChannelLabel, true)
	partialChannel.maxRetransmits = &partialReliability
	for _, channel := range []*controlChannelRecorder{
		newControlChannelRecorder(realtimev1.ControlDataChannelLabel, false),
		partialChannel,
	} {
		_, err := newPionControlReceiver(channel, &controlHandlerRecorder{}, "session-1", "rtc-1")
		if !errors.Is(err, ErrControlChannelInvalid) || channel.ReadyState() != pion.DataChannelStateClosed {
			t.Fatalf("invalid control channel: state=%s error=%v", channel.ReadyState(), err)
		}
	}
}

func newTestControlReceiver(t *testing.T, handler ControlCommandHandler) (*controlChannelRecorder, *PionControlReceiver) {
	t.Helper()
	channel := newControlChannelRecorder(realtimev1.ControlDataChannelLabel, true)
	receiver, err := newPionControlReceiver(channel, handler, "session-1", "rtc-1")
	if err != nil {
		t.Fatalf("newPionControlReceiver() error = %v", err)
	}
	t.Cleanup(func() { _ = receiver.Close(context.Background()) })
	return channel, receiver
}

func validControlModeSwitchRequest() realtimev1.ControlModeSwitchRequest {
	return realtimev1.ControlModeSwitchRequest{
		ProtocolVersion: realtimev1.ControlProtocolVersion,
		Type:            realtimev1.ControlMessageModeSwitch,
		RequestID:       "request-1",
		Command: realtimev1.ControlModeSwitchCommand{
			RuntimeInstanceID:  "runtime-1",
			OperationID:        "operation-1",
			ExpectedGeneration: 1,
			TargetMode:         realtimev1.ModeAssistant,
		},
	}
}

func mutateControlRequest(
	t *testing.T,
	request realtimev1.ControlModeSwitchRequest,
	mutate func(*realtimev1.ControlModeSwitchRequest),
) []byte {
	t.Helper()
	mutate(&request)
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return payload
}

type controlHandlerCall struct {
	sessionID    string
	connectionID string
	requestID    string
	command      realtimev1.ControlModeSwitchCommand
}

type controlHandlerRecorder struct {
	mu      sync.Mutex
	calls   []controlHandlerCall
	block   chan struct{}
	entered chan struct{}
}

func (h *controlHandlerRecorder) HandleModeSwitch(
	ctx context.Context,
	sessionID string,
	connectionID string,
	requestID string,
	command realtimev1.ControlModeSwitchCommand,
) realtimev1.ControlResponse {
	h.mu.Lock()
	h.calls = append(h.calls, controlHandlerCall{sessionID, connectionID, requestID, command})
	h.mu.Unlock()
	if h.entered != nil {
		select {
		case h.entered <- struct{}{}:
		default:
		}
	}
	if h.block != nil {
		select {
		case <-ctx.Done():
		case <-h.block:
		}
	}
	operationID := command.OperationID
	result := &realtimev1.SwitchModeResult{
		OperationID: command.OperationID,
		Status:      realtimev1.ModeSwitchApplied,
		State: realtimev1.ModeStateSnapshot{
			SessionID: sessionID, RuntimeInstanceID: command.RuntimeInstanceID,
			ActiveMode: command.TargetMode, Generation: command.ExpectedGeneration + 1,
			Phase: realtimev1.ModePhaseActive, LastOperationID: &operationID, UpdatedAt: time.Now().UTC(),
		},
	}
	return realtimev1.ControlResponse{
		ProtocolVersion: realtimev1.ControlProtocolVersion,
		Type:            realtimev1.ControlMessageModeSwitchResult, RequestID: requestID, Result: result,
	}
}

func (h *controlHandlerRecorder) CallCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.calls)
}

func (h *controlHandlerRecorder) OnlyCall(t *testing.T) controlHandlerCall {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.calls) != 1 {
		t.Fatalf("control handler calls = %d, want 1", len(h.calls))
	}
	return h.calls[0]
}

type cancelAwareControlHandler struct {
	entered  chan struct{}
	canceled chan struct{}
}

func (h *cancelAwareControlHandler) HandleModeSwitch(
	ctx context.Context,
	_, _, _ string,
	_ realtimev1.ControlModeSwitchCommand,
) realtimev1.ControlResponse {
	close(h.entered)
	<-ctx.Done()
	close(h.canceled)
	return protocolError("", realtimev1.ErrorControlUnavailable)
}

type controlChannelRecorder struct {
	mu                sync.Mutex
	label             string
	ordered           bool
	maxPacketLifeTime *uint16
	maxRetransmits    *uint16
	state             pion.DataChannelState
	onMessage         func(pion.DataChannelMessage)
	onClose           func()
	onError           func(error)
	responses         chan realtimev1.ControlResponse
	sendBlock         chan struct{}
}

func newControlChannelRecorder(label string, ordered bool) *controlChannelRecorder {
	return &controlChannelRecorder{
		label: label, ordered: ordered, state: pion.DataChannelStateOpen,
		responses: make(chan realtimev1.ControlResponse, controlQueueCapacity+4),
	}
}

func (c *controlChannelRecorder) Label() string                              { return c.label }
func (c *controlChannelRecorder) Ordered() bool                              { return c.ordered }
func (c *controlChannelRecorder) MaxPacketLifeTime() *uint16                 { return c.maxPacketLifeTime }
func (c *controlChannelRecorder) MaxRetransmits() *uint16                    { return c.maxRetransmits }
func (c *controlChannelRecorder) OnMessage(fn func(pion.DataChannelMessage)) { c.onMessage = fn }
func (c *controlChannelRecorder) OnClose(fn func())                          { c.onClose = fn }
func (c *controlChannelRecorder) OnError(fn func(error))                     { c.onError = fn }
func (c *controlChannelRecorder) ReadyState() pion.DataChannelState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}
func (c *controlChannelRecorder) SendText(payload string) error {
	if c.sendBlock != nil {
		<-c.sendBlock
	}
	var response realtimev1.ControlResponse
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		return err
	}
	c.responses <- response
	return nil
}
func (c *controlChannelRecorder) Close() error {
	c.mu.Lock()
	if c.state == pion.DataChannelStateClosed {
		c.mu.Unlock()
		return nil
	}
	c.state = pion.DataChannelStateClosed
	onClose := c.onClose
	c.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	return nil
}

func (c *controlChannelRecorder) Receive(t *testing.T, request realtimev1.ControlModeSwitchRequest, text bool) {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	c.ReceiveBytes(payload, text)
}

func (c *controlChannelRecorder) ReceiveBytes(payload []byte, text bool) {
	c.mu.Lock()
	handler := c.onMessage
	c.mu.Unlock()
	if handler != nil {
		handler(pion.DataChannelMessage{Data: payload, IsString: text})
	}
}

func (c *controlChannelRecorder) NextResponse(t *testing.T) realtimev1.ControlResponse {
	t.Helper()
	select {
	case response := <-c.responses:
		return response
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for control response")
		return realtimev1.ControlResponse{}
	}
}

var _ pionControlDataChannel = (*controlChannelRecorder)(nil)
