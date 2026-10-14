package compare

import (
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/result"
)

func TestResults(t *testing.T) {
	cert := &result.Certificate{Subject: "s", Issuer: "i", NotAfter: time.Unix(1, 0), Fingerprint: "f", DNSNames: []string{"a"}}
	base := result.Result{StatusCode: 200, TLSVersion: "TLS 1.3", BodySHA256: "a", BodySize: 1, Certificate: cert,
		Headers: []result.Header{{Name: "Location", Values: []string{"/a"}}, {Name: "Server", Values: []string{"old"}}}}
	tests := []struct {
		name      string
		mutate    func(*result.Result)
		ignore    []string
		wantField string
		wantCount int
	}{
		{name: "identical", mutate: func(*result.Result) {}, wantCount: 0},
		{name: "status", mutate: func(r *result.Result) { r.StatusCode = 404 }, wantField: "status", wantCount: 1},
		{name: "location", mutate: func(r *result.Result) { r.Headers[0].Values[0] = "/b" }, wantField: "header.location", wantCount: 1},
		{name: "ignored location", mutate: func(r *result.Result) { r.Headers[0].Values[0] = "/b" }, ignore: []string{"LOCATION"}, wantCount: 0},
		{name: "noise", mutate: func(r *result.Result) { r.Headers[1].Values[0] = "new" }, wantCount: 0},
		{name: "body", mutate: func(r *result.Result) { r.BodySHA256 = "b" }, wantField: "body", wantCount: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotNew := base
			gotNew.Headers = []result.Header{{Name: "Location", Values: []string{"/a"}}, {Name: "Server", Values: []string{"old"}}}
			test.mutate(&gotNew)
			got := Results("/", base, gotNew, test.ignore)
			if len(got) != test.wantCount {
				t.Fatalf("Results() = %#v", got)
			}
			if len(got) != 0 && got[0].Field != test.wantField {
				t.Fatalf("field = %q, want %q", got[0].Field, test.wantField)
			}
		})
	}
}
