package probe

/*
These differential cases intentionally document varco's response behavior
alongside net/http:

  - varco writes the request target byte-for-byte; net/http builds and
    normalizes its request target from a URL.
  - varco sends no Accept-Encoding and never decompresses response bodies;
    net/http's Transport may negotiate and transparently decode gzip.
  - varco skips up to eight interim 1xx response blocks before returning;
    a raw http.ReadResponse call surfaces the first 1xx, while a Transport
    normally continues to the final response.
  - trailers are compared by moving net/http's resp.Trailer values into
    "Trailer:<Canonical-Name>" headers, matching varco's documented folding.

Obs-fold behavior does not differ for these cases: the current Go net/http
client and varco both accept the continuation and expose the value with its
continuation whitespace folded to one space. The assertions compare that
shared result rather than rejecting the legacy wire form.
*/

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/result"
)

type responseShape struct {
	name   string
	method string
	wire   string
}

type responseSnapshot struct {
	status    int
	headers   []result.Header
	bodySize  int64
	bodySHA   string
}

func longValidHeaderResponse() string {
	prefix := "HTTP/1.1 200 OK\r\nContent-Length: 0\r\nX-Long: "
	suffix := "\r\n\r\n"
	valueBytes := maxHeaderBytes - 1 - len(prefix) - len(suffix)
	return prefix + strings.Repeat("a", valueBytes) + suffix
}

func TestDoMatchesNetHTTPResponseShapes(t *testing.T) {
	shapes := []responseShape{
		{
			name:   "content length",
			method: http.MethodGet,
			wire:   "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nX-Shape: length\r\n\r\nhello",
		},
		{
			name:   "chunked",
			method: http.MethodGet,
			wire:   "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n",
		},
		{
			name:   "chunked with trailers",
			method: http.MethodGet,
			wire:   "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nTrailer: X-Checksum, X-Trace\r\n\r\n5\r\nhello\r\n0\r\nX-Checksum: abc\r\nX-Trace: two\r\n\r\n",
		},
		{
			name:   "chunk extensions",
			method: http.MethodGet,
			wire:   "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3;name=\"value\";flag\r\nabc\r\n0;last\r\n\r\n",
		},
		{
			name:   "head content length",
			method: http.MethodHead,
			wire:   "HTTP/1.1 200 OK\r\nContent-Length: 9\r\nX-Shape: head\r\n\r\n",
		},
		{
			name:   "204 content length",
			method: http.MethodGet,
			wire:   "HTTP/1.1 204 No Content\r\nContent-Length: 5\r\nConnection: keep-alive\r\n\r\n",
		},
		{
			name:   "304 content length",
			method: http.MethodGet,
			wire:   "HTTP/1.1 304 Not Modified\r\nContent-Length: 5\r\nConnection: keep-alive\r\n\r\n",
		},
		{
			name:   "connection close until eof",
			method: http.MethodGet,
			wire:   "HTTP/1.1 200 OK\r\nConnection: close\r\n\r\nuntil-eof",
		},
		{
			name:   "multiple header values",
			method: http.MethodGet,
			wire:   "HTTP/1.1 200 OK\r\nSet-Cookie: a=1\r\nSet-Cookie: b=2\r\nX-Multi: first\r\nX-Multi: second\r\nContent-Length: 0\r\n\r\n",
		},
		{
			name:   "obs fold",
			method: http.MethodGet,
			wire:   "HTTP/1.1 200 OK\r\nX-Fold: first\r\n  second\r\nContent-Length: 4\r\n\r\nfold",
		},
		{
			name:   "interim then final",
			method: http.MethodGet,
			wire:   "HTTP/1.1 100 Continue\r\nX-Interim: yes\r\n\r\nHTTP/1.1 103 Early Hints\r\nLink: </style.css>; rel=preload\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nfinal",
		},
		{
			name:   "long valid header",
			method: http.MethodGet,
			wire:   longValidHeaderResponse(),
		},
		{
			name:   "eight bit header value",
			method: http.MethodGet,
			wire:   "HTTP/1.1 200 OK\r\nX-High: caf\xE9\r\nContent-Length: 0\r\n\r\n",
		},
		{
			name:   "unusual reason phrase",
			method: http.MethodGet,
			wire:   "HTTP/1.1 299 Unusual Reason / with; punctuation\r\nContent-Length: 0\r\n\r\n",
		},
		{
			name:   "empty reason phrase",
			method: http.MethodGet,
			wire:   "HTTP/1.1 200 \r\nContent-Length: 0\r\n\r\n",
		},
		{
			name:   "http 1.0",
			method: http.MethodGet,
			wire:   "HTTP/1.0 200 Fine\r\nContent-Length: 3\r\n\r\nold",
		},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			request := startExactResponse(t, shape.wire, shape.method)
			want := netHTTPResponseSnapshot(t, request)
			got, err := Do(context.Background(), request)
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			gotSnapshot := responseSnapshot{
				status:   got.StatusCode,
				headers:  got.Headers,
				bodySize: got.BodySize,
				bodySHA:  got.BodySHA256,
			}
			if gotSnapshot.status != want.status {
				t.Errorf("status = %d, net/http = %d", gotSnapshot.status, want.status)
			}
			if !reflect.DeepEqual(gotSnapshot.headers, want.headers) {
				t.Errorf("headers = %#v, net/http = %#v", gotSnapshot.headers, want.headers)
			}
			if gotSnapshot.bodySize != want.bodySize {
				t.Errorf("body size = %d, net/http = %d", gotSnapshot.bodySize, want.bodySize)
			}
			if gotSnapshot.bodySHA != want.bodySHA {
				t.Errorf("body SHA-256 = %q, net/http = %q", gotSnapshot.bodySHA, want.bodySHA)
			}
		})
	}
}

func startExactResponse(t *testing.T, wire, method string) Request {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				if deadline, ok := serverContext.Deadline(); ok {
					_ = conn.SetDeadline(deadline)
				}
				reader := bufio.NewReader(conn)
				for {
					line, readErr := reader.ReadString('\n')
					if readErr != nil || line == "\r\n" {
						break
					}
				}
				_, _ = io.WriteString(conn, wire)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		cancel()
	})
	return Request{
		Endpoint: endpointOf(t, listener.Addr().String()),
		Scheme:   "http",
		Host:     "h.example",
		Method:   method,
		Path:     "/",
		Timeout:  2 * time.Second,
	}
}

func netHTTPResponseSnapshot(t *testing.T, request Request) responseSnapshot {
	t.Helper()
	httpRequest, err := http.NewRequest(request.Method, "http://"+request.Endpoint.String()+request.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(httpRequest)
	if err != nil {
		t.Fatalf("net/http Do() error = %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		t.Fatalf("net/http body read error = %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("net/http body close error = %v", closeErr)
	}
	sum := sha256.Sum256(body)
	headers := referenceHeaders(response.Header, "")
	headers = append(headers, referenceHeaders(response.Trailer, "Trailer:")...)
	sort.Slice(headers, func(i, j int) bool { return headers[i].Name < headers[j].Name })
	return responseSnapshot{
		status:   response.StatusCode,
		headers:  headers,
		bodySize: int64(len(body)),
		bodySHA:  hex.EncodeToString(sum[:]),
	}
}

func referenceHeaders(headers http.Header, namePrefix string) []result.Header {
	captured := make([]result.Header, 0, len(headers))
	for name, values := range headers {
		captured = append(captured, result.Header{
			Name:   namePrefix + http.CanonicalHeaderKey(name),
			Values: append([]string(nil), values...),
		})
	}
	sort.Slice(captured, func(i, j int) bool { return captured[i].Name < captured[j].Name })
	return captured
}

func TestReadResponseRejectsOutOfRangeStatus(t *testing.T) {
	for _, status := range []string{"000", "099", "600", "999"} {
		t.Run(status, func(t *testing.T) {
			wire := "HTTP/1.1 " + status + " Invalid\r\nContent-Length: 0\r\n\r\n"
			response, err := readResponse(strings.NewReader(wire), http.MethodGet)
			if response != nil || !errors.Is(err, errInvalidResponse) {
				t.Fatalf("readResponse() = (%v, %v), want nil, %v", response, err, errInvalidResponse)
			}
		})
	}
}

func FuzzReadResponse(f *testing.F) {
	for _, seed := range []string{
		"HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\nx",
		"HTTP/1.1 200 OK\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n",
		"HTTP/1.1 200 OK\nContent-Length: 1\n\nx",
		"HTTP/1.1 200 OK\r\nX-Test: bad\x00value\r\nContent-Length: 0\r\n\r\n",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n7fffffffffffffff\r\n",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n-1\r\n",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: identity\r\n\r\n0\r\n\r\n",
		"HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n",
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, wire []byte) {
		header, headerErr := readHeaderBlock(bufio.NewReader(bytes.NewReader(wire)))
		if headerErr == nil && len(header) > maxHeaderBytes {
			t.Fatalf("header block size = %d, cap = %d", len(header), maxHeaderBytes)
		}
		if headerErr != nil && !errors.Is(headerErr, io.EOF) && !strings.Contains(headerErr.Error(), "response header exceeds") {
			t.Fatalf("unexpected header block error = %v", headerErr)
		}

		client, server := net.Pipe()
		defer func() { _ = client.Close() }()
		if err := client.SetDeadline(time.Now().Add(2*time.Second)); err != nil { //nolint:forbidigo // fuzz enforces a real connection deadline.
			t.Fatal(err)
		}
		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			defer func() { _ = server.Close() }()
			_, _ = io.Copy(server, bytes.NewReader(wire))
		}()

		type readResult struct {
			response *http.Response
			err      error
		}
		resultCh := make(chan readResult, 1)
		go func() {
			response, err := readResponse(client, http.MethodGet)
			resultCh <- readResult{response: response, err: err}
		}()
		select {
		case result := <-resultCh:
			if result.err == nil && (result.response == nil || result.response.StatusCode < 100 || result.response.StatusCode > 599) {
				status := -1
				if result.response != nil {
					status = result.response.StatusCode
				}
				t.Fatalf("successful status = %d, want 100..599", status)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("readResponse exceeded deadline")
		}
		_ = client.Close()
		<-writerDone
	})
}
