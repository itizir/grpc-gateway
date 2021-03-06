package runtime_test

import (
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/protobuf/proto"
	pb "github.com/grpc-ecosystem/grpc-gateway/examples/examplepb"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/grpc-ecosystem/grpc-gateway/runtime/internal"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type errorStringMarshaller struct {
	runtime.JSONPb
	errorState bool
}

func (em *errorStringMarshaller) Marshal(v interface{}) ([]byte, error) {
	m := v.(map[string]proto.Message)

	if err, isErr := m["error"]; isErr {
		em.errorState = true
		return []byte(err.(*internal.StreamError).Message), nil
	}

	em.errorState = false
	return em.JSONPb.Marshal(v)
}

func (em *errorStringMarshaller) ContentType() string {
	if em.errorState {
		return "text/plain"
	}
	return em.JSONPb.ContentType()
}

func TestForwardResponseStream(t *testing.T) {
	type msg struct {
		pb  proto.Message
		err error
	}
	tests := []struct {
		name       string
		msgs       []msg
		statusCode int
	}{{
		name: "encoding",
		msgs: []msg{
			{&pb.SimpleMessage{Id: "One"}, nil},
			{&pb.SimpleMessage{Id: "Two"}, nil},
		},
		statusCode: http.StatusOK,
	}, {
		name:       "empty",
		statusCode: http.StatusOK,
	}, {
		name:       "error",
		msgs:       []msg{{nil, grpc.Errorf(codes.OutOfRange, "400")}},
		statusCode: http.StatusBadRequest,
	}, {
		name: "stream_error",
		msgs: []msg{
			{&pb.SimpleMessage{Id: "One"}, nil},
			{&pb.SimpleMessage{Id: "Two"}, nil},
			{nil, grpc.Errorf(codes.OutOfRange, "400")},
		},
		statusCode: http.StatusOK,
	}}

	newTestRecv := func(t *testing.T, msgs []msg) func() (proto.Message, error) {
		var count int
		return func() (proto.Message, error) {
			if count == len(msgs) {
				return nil, io.EOF
			} else if count > len(msgs) {
				t.Errorf("recv() called %d times for %d messages", count, len(msgs))
			}
			count++
			msg := msgs[count-1]
			return msg.pb, msg.err
		}
	}
	ctx := runtime.NewServerMetadataContext(context.Background(), runtime.ServerMetadata{})
	marshaler := &errorStringMarshaller{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recv := newTestRecv(t, tt.msgs)
			req := httptest.NewRequest("GET", "http://example.com/foo", nil)
			resp := httptest.NewRecorder()

			runtime.ForwardResponseStream(ctx, runtime.NewServeMux(), marshaler, resp, req, recv)

			w := resp.Result()
			if w.StatusCode != tt.statusCode {
				t.Errorf("StatusCode %d want %d", w.StatusCode, tt.statusCode)
			}
			if h := w.Header.Get("Transfer-Encoding"); h != "chunked" {
				t.Errorf("ForwardResponseStream missing header chunked")
			}
			body, err := ioutil.ReadAll(w.Body)
			if err != nil {
				t.Errorf("Failed to read response body with %v", err)
			}
			w.Body.Close()

			var (
				want          []byte
				expectErrType bool
			)
			for i, msg := range tt.msgs {
				if msg.err != nil {
					if i == 0 {
						expectErrType = true
						want = []byte(msg.err.Error())
					}
					break
				}
				b, err := marshaler.Marshal(map[string]proto.Message{"result": msg.pb})
				if err != nil {
					t.Errorf("marshaler.Marshal() failed %v", err)
				}
				want = append(want, b...)
				want = append(want, marshaler.Delimiter()...)
			}

			if string(body) != string(want) {
				t.Errorf("ForwardResponseStream() = \"%s\" want \"%s\"", body, want)
			}

			marshaler.errorState = expectErrType
			if ct := w.Header.Get("Content-Type"); ct != marshaler.ContentType() {
				t.Errorf("Not the expected content type: %q want %q", ct, marshaler.ContentType())
			}
		})
	}
}

// A custom marshaler implementation, that doesn't implement the delimited interface
type CustomMarshaler struct {
	m *runtime.JSONPb
}

func (c *CustomMarshaler) Marshal(v interface{}) ([]byte, error)      { return c.m.Marshal(v) }
func (c *CustomMarshaler) Unmarshal(data []byte, v interface{}) error { return c.m.Unmarshal(data, v) }
func (c *CustomMarshaler) NewDecoder(r io.Reader) runtime.Decoder     { return c.m.NewDecoder(r) }
func (c *CustomMarshaler) NewEncoder(w io.Writer) runtime.Encoder     { return c.m.NewEncoder(w) }
func (c *CustomMarshaler) ContentType() string                        { return c.m.ContentType() }

func TestForwardResponseStreamCustomMarshaler(t *testing.T) {
	type msg struct {
		pb  proto.Message
		err error
	}
	tests := []struct {
		name       string
		msgs       []msg
		statusCode int
	}{{
		name: "encoding",
		msgs: []msg{
			{&pb.SimpleMessage{Id: "One"}, nil},
			{&pb.SimpleMessage{Id: "Two"}, nil},
		},
		statusCode: http.StatusOK,
	}, {
		name:       "empty",
		statusCode: http.StatusOK,
	}, {
		name:       "error",
		msgs:       []msg{{nil, grpc.Errorf(codes.OutOfRange, "400")}},
		statusCode: http.StatusBadRequest,
	}, {
		name: "stream_error",
		msgs: []msg{
			{&pb.SimpleMessage{Id: "One"}, nil},
			{nil, grpc.Errorf(codes.OutOfRange, "400")},
		},
		statusCode: http.StatusOK,
	}}

	newTestRecv := func(t *testing.T, msgs []msg) func() (proto.Message, error) {
		var count int
		return func() (proto.Message, error) {
			if count == len(msgs) {
				return nil, io.EOF
			} else if count > len(msgs) {
				t.Errorf("recv() called %d times for %d messages", count, len(msgs))
			}
			count++
			msg := msgs[count-1]
			return msg.pb, msg.err
		}
	}
	ctx := runtime.NewServerMetadataContext(context.Background(), runtime.ServerMetadata{})
	marshaler := &CustomMarshaler{&runtime.JSONPb{}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recv := newTestRecv(t, tt.msgs)
			req := httptest.NewRequest("GET", "http://example.com/foo", nil)
			resp := httptest.NewRecorder()

			runtime.ForwardResponseStream(ctx, runtime.NewServeMux(), marshaler, resp, req, recv)

			w := resp.Result()
			if w.StatusCode != tt.statusCode {
				t.Errorf("StatusCode %d want %d", w.StatusCode, tt.statusCode)
			}
			if h := w.Header.Get("Transfer-Encoding"); h != "chunked" {
				t.Errorf("ForwardResponseStream missing header chunked")
			}
			body, err := ioutil.ReadAll(w.Body)
			if err != nil {
				t.Errorf("Failed to read response body with %v", err)
			}
			w.Body.Close()

			var want []byte
			for _, msg := range tt.msgs {
				if msg.err != nil {
					t.Skip("checking erorr encodings")
				}
				b, err := marshaler.Marshal(map[string]proto.Message{"result": msg.pb})
				if err != nil {
					t.Errorf("marshaler.Marshal() failed %v", err)
				}
				want = append(want, b...)
				want = append(want, "\n"...)
			}

			if string(body) != string(want) {
				t.Errorf("ForwardResponseStream() = \"%s\" want \"%s\"", body, want)
			}
		})
	}
}
