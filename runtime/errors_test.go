package runtime_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type brokenMarshaler struct {
	runtime.JSONBuiltin
}

func (brokenMarshaler) Marshal(interface{}) ([]byte, error) {
	return nil, errors.New("broken test marshaler")
}

func (brokenMarshaler) ContentType() string {
	// fallbackType
	return "application/json"
}

type changingTypeMarshaler struct {
	runtime.JSONBuiltin
	called bool
}

func (m *changingTypeMarshaler) Marshal(v interface{}) ([]byte, error) {
	m.called = true
	return m.JSONBuiltin.Marshal(v)
}

func (m *changingTypeMarshaler) ContentType() string {
	if m.called {
		return "application/json"
	}
	return "some other type"
}

func TestDefaultHTTPError(t *testing.T) {
	ctx := context.Background()

	for _, spec := range []struct {
		err       error
		status    int
		msg       string
		marshaler runtime.Marshaler
	}{
		{
			err:       fmt.Errorf("example error"),
			status:    http.StatusInternalServerError,
			msg:       "example error",
			marshaler: &runtime.JSONBuiltin{},
		},
		{
			err:       status.Error(codes.NotFound, "no such resource"),
			status:    http.StatusNotFound,
			msg:       "no such resource",
			marshaler: &runtime.JSONBuiltin{},
		},
		{
			err:       fmt.Errorf("can't marshal me anyway"),
			status:    http.StatusInternalServerError,
			msg:       "failed to marshal error message",
			marshaler: &brokenMarshaler{},
		},
		{
			err:       fmt.Errorf("example error"),
			status:    http.StatusInternalServerError,
			msg:       "example error",
			marshaler: &changingTypeMarshaler{},
		},
	} {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("", "", nil) // Pass in an empty request to match the signature
		runtime.DefaultHTTPError(ctx, &runtime.ServeMux{}, spec.marshaler, w, req, spec.err)

		if got, want := w.Header().Get("Content-Type"), "application/json"; got != want {
			t.Errorf(`w.Header().Get("Content-Type") = %q; want %q; on spec.err=%v`, got, want, spec.err)
		}
		if got, want := w.Code, spec.status; got != want {
			t.Errorf("w.Code = %d; want %d", got, want)
		}

		body := make(map[string]interface{})
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Errorf("json.Unmarshal(%q, &body) failed with %v; want success", w.Body.Bytes(), err)
			continue
		}

		if got, want := body["error"].(string), spec.msg; !strings.Contains(got, want) {
			t.Errorf(`body["error"] = %q; want %q; on spec.err=%v`, got, want, spec.err)
		}
	}
}
