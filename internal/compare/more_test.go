package compare

import (
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/result"
)

// Source: RFC 9110 section 5.3 (field order) and MDN header docs for each field.
func TestResultsFields(t *testing.T) {
	cert := func() *result.Certificate {
		return &result.Certificate{Subject: "s", Issuer: "i", NotAfter: time.Unix(1, 0), Fingerprint: "f", DNSNames: []string{"a"}}
	}
	mk := func() result.Result {
		return result.Result{StatusCode: 200, TLSVersion: "TLS 1.3", Certificate: cert(), BodySHA256: "a"}
	}
	tests := []struct {
		name   string
		mutate func(*result.Result)
		ignore []string
		fields []string
		code   diag.Code
	}{
		{"tls version", func(r *result.Result) { r.TLSVersion = "TLS 1.2" }, nil, []string{"tls.version"}, diag.CodeCompareTLSVersion},
		{"cert fingerprint", func(r *result.Result) { r.Certificate.Fingerprint = "g" }, nil, []string{"certificate.fingerprint"}, diag.CodeCompareCertificate},
		{"cert sans", func(r *result.Result) { r.Certificate.DNSNames = []string{"b"} }, nil, []string{"certificate.dns_sans"}, diag.CodeCompareCertificate},
		{"cert missing", func(r *result.Result) { r.Certificate = nil }, nil, []string{"certificate"}, diag.CodeCompareCertificate},
		{"hsts", func(r *result.Result) {
			r.Headers = []result.Header{{Name: "Strict-Transport-Security", Values: []string{"max-age=1"}}}
		}, nil, []string{"header.strict-transport-security"}, diag.CodeCompareHeader},
		{"cors", func(r *result.Result) {
			r.Headers = []result.Header{{Name: "Access-Control-Allow-Origin", Values: []string{"*"}}}
		}, nil, []string{"header.access-control-allow-origin"}, diag.CodeCompareHeader},
		{"ignored cors", func(r *result.Result) {
			r.Headers = []result.Header{{Name: "Access-Control-Allow-Origin", Values: []string{"*"}}}
		}, []string{"Access-Control-Allow-Origin"}, nil, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldResult, newResult := mk(), mk()
			test.mutate(&newResult)
			got := Results("/", oldResult, newResult, test.ignore, BodyPolicyHash)
			if len(got) != len(test.fields) {
				t.Fatalf("got %#v", got)
			}
			for i, field := range test.fields {
				if got[i].Field != field || got[i].Code != test.code {
					t.Fatalf("got[%d] = %#v", i, got[i])
				}
			}
		})
	}
}

func TestResultsSorted(t *testing.T) {
	a := result.Result{StatusCode: 200, BodySHA256: "a"}
	b := result.Result{StatusCode: 500, BodySHA256: "b"}
	got := Results("/", a, b, nil, BodyPolicyHash)
	if len(got) != 2 || got[0].Field != "body" || got[1].Field != "status" {
		t.Fatalf("got %#v", got)
	}
}

// Source: RFC 9110 section 5.2/5.3: repeated fields are an ordered list; an
// absent field is not the same as one with an empty value.
func TestResultsHeaderValues(t *testing.T) {
	with := func(values ...string) result.Result {
		return result.Result{StatusCode: 200, Headers: []result.Header{{Name: "Access-Control-Allow-Methods", Values: values}}}
	}
	tests := []struct {
		name             string
		old, new         result.Result
		wantOld, wantNew string
		diffs            int
	}{
		{"joined vs split", with("GET, POST"), with("GET", "POST"), `GET, POST`, `"GET", "POST"`, 1},
		{"same split", with("GET", "POST"), with("GET", "POST"), "", "", 0},
		{"reordered", with("GET", "POST"), with("POST", "GET"), `"GET", "POST"`, `"POST", "GET"`, 1},
		{"absent vs empty", result.Result{StatusCode: 200}, with(""), "(absent)", `""`, 1},
		{"absent both", result.Result{StatusCode: 200}, result.Result{StatusCode: 200}, "", "", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Results("/", test.old, test.new, nil, BodyPolicyHash)
			if len(got) != test.diffs {
				t.Fatalf("got %#v", got)
			}
			if test.diffs == 1 && (got[0].Old != test.wantOld || got[0].New != test.wantNew) {
				t.Fatalf("got %#v", got[0])
			}
		})
	}
}

// Source: RFC 9110 section 8.4: Content-Encoding changes the wire body and is compared.
func TestResultsContentEncoding(t *testing.T) {
	a := result.Result{StatusCode: 200}
	b := result.Result{StatusCode: 200, Headers: []result.Header{{Name: "Content-Encoding", Values: []string{"gzip"}}}}
	got := Results("/", a, b, nil, BodyPolicyHash)
	if len(got) != 1 || got[0].Field != "header.content-encoding" {
		t.Fatalf("got %#v", got)
	}
}

// Source: RFC 6265 sections 4.1.1 and 5.2: a Set-Cookie field carries one
// cookie value plus attributes; migration comparison intentionally ignores the
// value and compares the named cookie's security and scope attributes.
func TestResultsSetCookieAttributes(t *testing.T) {
	with := func(values ...string) result.Result {
		return result.Result{StatusCode: 200, Headers: []result.Header{{Name: "Set-Cookie", Values: values}}}
	}
	tests := []struct {
		name     string
		old, new result.Result
		ignore   []string
		wantDiff bool
	}{
		{"value only", with("session=old-secret; Secure; HttpOnly; SameSite=Lax; Path=/"), with("session=new-secret; Secure; HttpOnly; SameSite=Lax; Path=/"), nil, false},
		{"Secure dropped", with("session=a; Secure"), with("session=b"), nil, true},
		{"SameSite changed", with("session=a; SameSite=Lax"), with("session=b; SameSite=Strict"), nil, true},
		{"cookie missing", with("session=a; HttpOnly"), result.Result{StatusCode: 200}, nil, true},
		{"attribute presence", with("session=a"), with("session=b; Max-Age=60; Expires=Wed, 21 Oct 2030 07:28:00 GMT; Partitioned; Secure"), nil, true},
		{"name set sorted", with("z=a", "a=b"), with("z=c"), nil, true},
		{"ignored", with("session=a; Secure"), with("session=b"), []string{"SET-cookie"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Results("/", test.old, test.new, test.ignore, BodyPolicyHash)
			if (len(got) != 0) != test.wantDiff {
				t.Fatalf("Results() = %#v", got)
			}
			if !test.wantDiff {
				return
			}
			if len(got) != 1 || got[0].Code != diag.CodeCompareCookie || got[0].Field != "header.set-cookie" {
				t.Fatalf("Results() = %#v", got)
			}
			if strings.Contains(got[0].Old, "old-secret") || strings.Contains(got[0].New, "new-secret") || strings.Contains(got[0].Old, "session=a") || strings.Contains(got[0].New, "session=b") {
				t.Fatalf("cookie value leaked in %#v", got[0])
			}
		})
	}
}
