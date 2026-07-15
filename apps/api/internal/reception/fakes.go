package reception

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type FakeAccessAuthorizer struct {
	Denied        bool
	Expired       bool
	ScopeMismatch bool
	Calls         int
}

func (f *FakeAccessAuthorizer) Authorize(_ context.Context, req AuthorizeRequest) (AccessContextView, error) {
	f.Calls++
	if f.Expired {
		return AccessContextView{}, businessError(CodeAccessContextInvalid, "访问上下文已过期或无效。")
	}
	if f.Denied {
		return AccessContextView{}, businessError(CodeAccessDenied, "当前工作人员无权执行此操作。")
	}
	if f.ScopeMismatch || (req.OrganizationID != "" && req.OrganizationID != "trial-org") ||
		(req.ServicePointID != "" && req.ServicePointID != "service-point-001") ||
		(req.ServiceWindowID != "" && req.ServiceWindowID != "window-001") {
		return AccessContextView{}, businessError(CodeOrganizationScopeMismatch, "工作人员访问范围与目标机构不匹配。")
	}
	return AccessContextView{
		OperatorID: "staff-demo-001", OrganizationID: "trial-org",
		ServicePointID: "service-point-001", ServiceWindowID: "window-001",
	}, nil
}

type FakeOrganizationConfigReader struct {
	Status  string
	Version string
}

func (f *FakeOrganizationConfigReader) GetPublishedConfig(_ context.Context, req PublishedConfigRequest) (OrganizationConfigView, error) {
	status := f.Status
	if status == "" {
		status = "published"
	}
	version := f.Version
	if version == "" {
		version = "config-v1"
	}
	if status != "published" {
		return OrganizationConfigView{}, businessError(CodeConfigNotPublished, "机构配置尚未发布。")
	}
	if req.ExpectedVersion != version {
		return OrganizationConfigView{}, businessError(CodeConfigVersionMismatch, "机构配置版本不匹配。")
	}
	return OrganizationConfigView{OrganizationID: "trial-org", Version: version, Status: status}, nil
}

type FakeProcessingGate struct {
	ReceptionAllowed            bool
	RealtimeAudioAllowed        bool
	RecordingPersistenceAllowed bool
	Expired                     bool
}

func DefaultFakeProcessingGate() *FakeProcessingGate {
	return &FakeProcessingGate{ReceptionAllowed: true, RealtimeAudioAllowed: true, RecordingPersistenceAllowed: false}
}

func (f *FakeProcessingGate) GetProcessingContext(_ context.Context, ref string) (ProcessingContextView, error) {
	return ProcessingContextView{
		Ref: ref, ReceptionAllowed: f.ReceptionAllowed, RealtimeAudioAllowed: f.RealtimeAudioAllowed,
		RecordingPersistenceAllowed: f.RecordingPersistenceAllowed, Expired: f.Expired,
	}, nil
}

type FakeMediaAdapter struct {
	mu          sync.Mutex
	Attached    map[string]AttachMediaRequest
	AttachCalls int
	DetachCalls int
}

func NewFakeMediaAdapter() *FakeMediaAdapter {
	return &FakeMediaAdapter{Attached: make(map[string]AttachMediaRequest)}
}

func (f *FakeMediaAdapter) Attach(_ context.Context, req AttachMediaRequest) (AttachMediaAdapterResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AttachCalls++
	if req.Scenario == FakeScenarioAttachFailure {
		return AttachMediaAdapterResult{}, fmt.Errorf("deterministic fake attach failure")
	}
	f.Attached[req.BindingID] = req
	return AttachMediaAdapterResult{}, nil
}

func (f *FakeMediaAdapter) Detach(_ context.Context, req DetachMediaRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DetachCalls++
	if req.Scenario == FakeScenarioDetachFailure {
		return fmt.Errorf("deterministic fake detach failure")
	}
	delete(f.Attached, req.BindingID)
	return nil
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type FixedClock struct {
	mu       sync.Mutex
	NowValue time.Time
}

func (f *FixedClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	value := f.NowValue
	f.NowValue = f.NowValue.Add(time.Second)
	return value
}

type SequentialIDGenerator struct{ value atomic.Int64 }

func (g *SequentialIDGenerator) NewID(prefix string) string {
	return fmt.Sprintf("%s-%06d", prefix, g.value.Add(1))
}

type FakeMediaResourceCleaner struct {
	mu      sync.Mutex
	Cleaned []string
	Fail    bool
}

func (f *FakeMediaResourceCleaner) Clean(_ context.Context, bindingID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail {
		return fmt.Errorf("deterministic cleaner failure")
	}
	f.Cleaned = append(f.Cleaned, bindingID)
	return nil
}

func (f *FakeMediaResourceCleaner) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Cleaned)
}
