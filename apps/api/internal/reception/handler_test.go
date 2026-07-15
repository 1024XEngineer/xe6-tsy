package reception

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type handlerFixture struct {
	system *testSystem
	router http.Handler
}

func newHandlerFixture() handlerFixture {
	system := newTestSystem()
	module := &Module{handler: NewHandler(system.service), Store: system.store}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	module.Register(router.Group("/api/v1"))
	return handlerFixture{system: system, router: router}
}

func requestJSON(t *testing.T, router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if raw, ok := body.([]byte); ok {
		payload = raw
	} else if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func responseData(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response JSON: %v: %s", err, recorder.Body.String())
	}
	return body
}

func TestHandlerCreateReturns201AndGetReturns200(t *testing.T) {
	fixture := newHandlerFixture()
	created := requestJSON(t, fixture.router, http.MethodPost, "/api/v1/reception/sessions", createCommand("http-create"))
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", created.Code, created.Body.String())
	}
	data := responseData(t, created)["data"].(map[string]any)
	got := requestJSON(t, fixture.router, http.MethodGet, "/api/v1/reception/sessions/"+data["session_id"].(string), nil)
	if got.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", got.Code, got.Body.String())
	}
}

func TestHandlerInvalidJSONReturns400(t *testing.T) {
	fixture := newHandlerFixture()
	got := requestJSON(t, fixture.router, http.MethodPost, "/api/v1/reception/sessions", []byte("{"))
	if got.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", got.Code, got.Body.String())
	}
}

func TestHandlerMissingSessionReturns404(t *testing.T) {
	fixture := newHandlerFixture()
	got := requestJSON(t, fixture.router, http.MethodGet, "/api/v1/reception/sessions/missing", nil)
	if got.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", got.Code, got.Body.String())
	}
}

func TestHandlerVersionConflictReturns409(t *testing.T) {
	fixture := newHandlerFixture()
	created := createSession(t, fixture.system, "version-http")
	got := requestJSON(t, fixture.router, http.MethodPost, "/api/v1/reception/sessions/"+created.SessionID+"/start", StartReceptionSessionCommand{AccessContextRef: "access-demo", ExpectedVersion: 99, IdempotencyKey: "start-http-version"})
	if got.Code != http.StatusConflict {
		t.Fatalf("status = %d: %s", got.Code, got.Body.String())
	}
}

func TestHandlerAccessDeniedReturns403(t *testing.T) {
	fixture := newHandlerFixture()
	fixture.system.authorizer.Denied = true
	got := requestJSON(t, fixture.router, http.MethodPost, "/api/v1/reception/sessions", createCommand("denied-http"))
	if got.Code != http.StatusForbidden {
		t.Fatalf("status = %d: %s", got.Code, got.Body.String())
	}
}

func TestHandlerAttachFailureReturnsDegradation(t *testing.T) {
	fixture := newHandlerFixture()
	started := startSession(t, fixture.system, createSession(t, fixture.system, "create-http-af"), "start-http-af")
	got := requestJSON(t, fixture.router, http.MethodPost, "/api/v1/reception/sessions/"+started.Session.SessionID+"/media-tracks", AttachFakeMediaTrackCommand{AccessContextRef: "access-demo", ExpectedSessionVersion: started.Session.Version, IdempotencyKey: "attach-http-af", TrackRef: "fake-track", Scenario: FakeScenarioAttachFailure})
	if got.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d: %s", got.Code, got.Body.String())
	}
	body := responseData(t, got)
	degradation := body["degradation"].(map[string]any)
	if degradation["mode"] != "manual_text" || degradation["session_remains_active"] != true {
		t.Fatalf("degradation = %#v", degradation)
	}
}

func TestHandlerIdempotentCreateDoesNotDuplicateEntity(t *testing.T) {
	fixture := newHandlerFixture()
	first := requestJSON(t, fixture.router, http.MethodPost, "/api/v1/reception/sessions", createCommand("http-idem"))
	second := requestJSON(t, fixture.router, http.MethodPost, "/api/v1/reception/sessions", createCommand("http-idem"))
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated || fixture.system.store.SessionCount() != 1 || len(fixture.system.store.Events()) != 1 {
		t.Fatalf("statuses = %d/%d count = %d", first.Code, second.Code, fixture.system.store.SessionCount())
	}
}
