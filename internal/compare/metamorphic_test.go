package compare

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/redact"
	"github.com/HarnageaGabriel/varco/internal/result"
)

// metamorphicSeed is the fixed seed of every generated case, so a failure is
// reproducible and the suite never flakes (CONVENTIONS.md: same input, same
// output).
const metamorphicSeed int64 = 20240917

// metamorphicPolicies are every accepted --body value plus the empty default
// (internal/compare/compare.go:24-53).
var metamorphicPolicies = []BodyPolicy{"", BodyPolicy2xx, BodyPolicyHash, BodyPolicyNone}

// metamorphicRand returns the generator of one case. Every case gets its own
// stream, so adding a case never changes the values of the other cases.
func metamorphicRand(caseIndex int) *rand.Rand {
	return rand.New(rand.NewSource(metamorphicSeed + int64(caseIndex)))
}

// randomStatus picks a status from the 2xx, 3xx, 4xx and 5xx ranges so the
// body policy branches are all exercised.
func randomStatus(rnd *rand.Rand) int {
	statuses := []int{200, 201, 204, 301, 302, 400, 404, 500}
	return statuses[rnd.Intn(len(statuses))]
}

// randomTLSVersion picks a TLS protocol name, including the empty value of a
// plaintext answer.
func randomTLSVersion(rnd *rand.Rand) string {
	versions := []string{"", "TLS 1.2", "TLS 1.3"}
	return versions[rnd.Intn(len(versions))]
}

// randomHash returns a body hash of the same shape the prober stores.
func randomHash(rnd *rand.Rand) string {
	return fmt.Sprintf("%064x", rnd.Uint64())
}

// randomCase rewrites a header name with another case. Field names are
// case-insensitive (RFC 9110 section 5.1).
func randomCase(rnd *rand.Rand, name string) string {
	switch rnd.Intn(3) {
	case 0:
		return strings.ToLower(name)
	case 1:
		return strings.ToUpper(name)
	default:
		return strings.ToUpper(name[:1]) + name[1:]
	}
}

// randomLocation returns a redirect target, some of them carrying a secret in
// the query (docs/adr/0006-redaction.md).
func randomLocation(rnd *rand.Rand) string {
	switch rnd.Intn(4) {
	case 0:
		return fmt.Sprintf("/path/%d", rnd.Intn(100))
	case 1:
		return fmt.Sprintf("https://api.example.com/cb?code=secret-%d", rnd.Intn(1000))
	case 2:
		return fmt.Sprintf("https://api.example.com/redirect?state=%d", rnd.Intn(1000))
	default:
		return fmt.Sprintf("https://api.example.com/path/%d", rnd.Intn(100))
	}
}

// randomCookieValues returns Set-Cookie values with distinct cookie names, so
// the order of the values cannot change the compared set of cookies.
func randomCookieValues(rnd *rand.Rand) []string {
	count := 1 + rnd.Intn(3)
	values := make([]string, 0, count)
	for index := range count {
		parts := []string{fmt.Sprintf("cookie%d=value-%d", index, rnd.Intn(10000))}
		if rnd.Intn(2) == 0 {
			parts = append(parts, "Secure")
		}
		if rnd.Intn(2) == 0 {
			parts = append(parts, "HttpOnly")
		}
		switch rnd.Intn(4) {
		case 0:
			parts = append(parts, "SameSite=Lax")
		case 1:
			parts = append(parts, "SameSite=Strict")
		case 2:
			parts = append(parts, "SameSite=None")
		}
		if rnd.Intn(2) == 0 {
			parts = append(parts, "Path=/")
		}
		if rnd.Intn(2) == 0 {
			parts = append(parts, "Domain=example.com")
		}
		if rnd.Intn(3) == 0 {
			parts = append(parts, "Max-Age=60")
		}
		if rnd.Intn(3) == 0 {
			parts = append(parts, "Expires=Wed, 21 Oct 2030 07:28:00 GMT")
		}
		if rnd.Intn(4) == 0 {
			parts = append(parts, "Partitioned")
		}
		values = append(values, strings.Join(parts, "; "))
	}
	return values
}

// randomHeaderNames lists every header the comparator can compare: Location,
// Set-Cookie (internal/compare/compare.go:95,98) and the security headers of
// comparedHeaders (internal/compare/compare.go:64-70).
func randomHeaderNames() []string {
	names := make([]string, 0, len(comparedHeaders)+2)
	names = append(names, "location", "set-cookie")
	names = append(names, comparedHeaders...)
	return names
}

// randomValues returns the values of one header, several of them for a
// repeated header.
func randomValues(rnd *rand.Rand, name string) []string {
	switch name {
	case "location":
		return []string{randomLocation(rnd)}
	case "set-cookie":
		return randomCookieValues(rnd)
	default:
		count := 1 + rnd.Intn(3)
		values := make([]string, 0, count)
		for index := range count {
			values = append(values, fmt.Sprintf("%s-value-%d", name, index))
		}
		return values
	}
}

// randomCertificate returns a leaf certificate with every compared field set.
func randomCertificate(rnd *rand.Rand) *result.Certificate {
	return &result.Certificate{
		Subject:     fmt.Sprintf("CN=host-%d.example.com", rnd.Intn(100)),
		Issuer:      fmt.Sprintf("CN=issuer-%d.example.com", rnd.Intn(100)),
		Serial:      fmt.Sprintf("%d", rnd.Intn(100000)),
		NotBefore:   time.Unix(int64(rnd.Intn(2000000000)), 0).UTC(),
		NotAfter:    time.Unix(int64(2000000000+rnd.Intn(2000000000)), 0).UTC(),
		Fingerprint: fmt.Sprintf("%064x", rnd.Uint64()),
		DNSNames:    []string{fmt.Sprintf("host-%d.example.com", rnd.Intn(100))},
	}
}

// randomResult builds one pseudo-random observation. The prober stores one
// entry per canonical header name (internal/probe/probe.go:270), so the
// generated names are unique case-insensitively.
func randomResult(rnd *rand.Rand) result.Result {
	captured := result.Result{
		StatusCode: randomStatus(rnd),
		TLSVersion: randomTLSVersion(rnd),
		BodySHA256: randomHash(rnd),
		BodySize:   int64(rnd.Intn(1 << 20)),
		Truncated:  rnd.Intn(4) == 0,
	}
	for _, name := range randomHeaderNames() {
		if rnd.Intn(4) == 0 {
			continue
		}
		captured.Headers = append(captured.Headers, result.Header{
			Name:   randomCase(rnd, name),
			Values: randomValues(rnd, name),
		})
	}
	if rnd.Intn(5) != 0 {
		captured.Certificate = randomCertificate(rnd)
	}
	return captured
}

// deepCopyResult copies an observation, including the header values and the
// certificate, so the copy shares no state with the original.
func deepCopyResult(captured result.Result) result.Result {
	copied := captured
	copied.Headers = make([]result.Header, 0, len(captured.Headers))
	for _, header := range captured.Headers {
		copied.Headers = append(copied.Headers, result.Header{
			Name:   header.Name,
			Values: append([]string(nil), header.Values...),
		})
	}
	if captured.Certificate != nil {
		certificate := *captured.Certificate
		certificate.DNSNames = append([]string(nil), captured.Certificate.DNSNames...)
		copied.Certificate = &certificate
	}
	return copied
}

// shuffleResult returns a copy of an observation whose header list is in
// another order.
func shuffleResult(rnd *rand.Rand, captured result.Result) result.Result {
	shuffled := deepCopyResult(captured)
	rnd.Shuffle(len(shuffled.Headers), func(i, j int) {
		shuffled.Headers[i], shuffled.Headers[j] = shuffled.Headers[j], shuffled.Headers[i]
	})
	return shuffled
}

// recaseResult returns a copy of an observation whose header names are
// written with another case.
func recaseResult(rnd *rand.Rand, captured result.Result) result.Result {
	recased := deepCopyResult(captured)
	for i := range recased.Headers {
		recased.Headers[i].Name = randomCase(rnd, recased.Headers[i].Name)
	}
	return recased
}

// redactLocation applies the report layer redaction to the Location values of
// an observation (internal/redact/url.go:13, internal/report/report.go:69-79).
func redactLocation(captured result.Result) result.Result {
	redacted := deepCopyResult(captured)
	for i := range redacted.Headers {
		if !strings.EqualFold(redacted.Headers[i].Name, "location") {
			continue
		}
		for j, value := range redacted.Headers[i].Values {
			redacted.Headers[i].Values[j] = redact.URL(value)
		}
	}
	return redacted
}

// differenceFields returns the sorted field names of the differences.
func differenceFields(differences []Difference) []string {
	fields := make([]string, 0, len(differences))
	for _, difference := range differences {
		fields = append(fields, difference.Field)
	}
	slices.Sort(fields)
	return fields
}

// differenceByField indexes the differences by field name. Every field is
// reported at most once by the comparator.
func differenceByField(differences []Difference) map[string]Difference {
	indexed := make(map[string]Difference, len(differences))
	for _, difference := range differences {
		indexed[difference.Field] = difference
	}
	return indexed
}

// assertSortedDifferences checks the output order of the comparator, which is
// sorted by path, then field, then code (internal/compare/compare.go:121-129).
func assertSortedDifferences(t *testing.T, differences []Difference) {
	t.Helper()
	for i := 1; i < len(differences); i++ {
		previous, current := differences[i-1], differences[i]
		if previous.Path > current.Path {
			t.Fatalf("differences are not sorted by path: %#v then %#v", previous, current)
		}
		if previous.Path == current.Path && previous.Field > current.Field {
			t.Fatalf("differences are not sorted by field: %#v then %#v", previous, current)
		}
		if previous.Path == current.Path && previous.Field == current.Field && previous.Code > current.Code {
			t.Fatalf("differences are not sorted by code: %#v then %#v", previous, current)
		}
	}
}

// TestMetamorphicIdentity checks the reflexive property of the comparator: an
// observation compared with a deep copy of itself reports no difference, under
// every body policy (internal/compare/compare.go:44-53,74-131).
func TestMetamorphicIdentity(t *testing.T) {
	for caseIndex := range 100 {
		rnd := metamorphicRand(caseIndex)
		captured := randomResult(rnd)
		copied := deepCopyResult(captured)
		for _, policy := range metamorphicPolicies {
			differences := Results("/identity", copied, captured, nil, policy)
			if len(differences) != 0 {
				t.Fatalf("case %d policy %q: differences = %#v", caseIndex, policy, differences)
			}
		}
	}
}

// TestMetamorphicSymmetry checks that the comparison is symmetric: swapping
// the two observations reports the same fields, with the old and the new value
// swapped. Every difference is built from the old and the new value in the
// same order (internal/compare/compare.go:93-120), so no compared field is
// intentionally asymmetric.
func TestMetamorphicSymmetry(t *testing.T) {
	for caseIndex := range 100 {
		rnd := metamorphicRand(caseIndex)
		oldResult := randomResult(rnd)
		newResult := randomResult(rnd)
		for _, policy := range metamorphicPolicies {
			forward := Results("/symmetry", oldResult, newResult, nil, policy)
			backward := Results("/symmetry", newResult, oldResult, nil, policy)
			if len(forward) != len(backward) {
				t.Fatalf("case %d policy %q: %d differences forward, %d backward", caseIndex, policy, len(forward), len(backward))
			}
			for i := range forward {
				if forward[i].Field != backward[i].Field || forward[i].Code != backward[i].Code {
					t.Fatalf("case %d policy %q: forward %#v, backward %#v", caseIndex, policy, forward[i], backward[i])
				}
				if forward[i].Old != backward[i].New || forward[i].New != backward[i].Old {
					t.Fatalf("case %d policy %q: forward %#v, backward %#v", caseIndex, policy, forward[i], backward[i])
				}
			}
		}
	}
}

// TestMetamorphicHeaderCaseAndOrderInsensitivity checks that the order of the
// header list and the case of the header names do not change the differences.
// Field names are case-insensitive (RFC 9110 section 5.1) and the lookup is
// case-insensitive too (internal/compare/compare.go:220-227).
func TestMetamorphicHeaderCaseAndOrderInsensitivity(t *testing.T) {
	for caseIndex := range 100 {
		rnd := metamorphicRand(caseIndex)
		oldResult := randomResult(rnd)
		newResult := randomResult(rnd)
		want := Results("/headers", oldResult, newResult, nil, BodyPolicyHash)
		shuffled := Results("/headers", shuffleResult(rnd, oldResult), shuffleResult(rnd, newResult), nil, BodyPolicyHash)
		if !reflect.DeepEqual(shuffled, want) {
			t.Fatalf("case %d: the header order changed the differences:\n got %#v\nwant %#v", caseIndex, shuffled, want)
		}
		recased := Results("/headers", recaseResult(rnd, oldResult), recaseResult(rnd, newResult), nil, BodyPolicyHash)
		if !reflect.DeepEqual(recased, want) {
			t.Fatalf("case %d: the header name case changed the differences:\n got %#v\nwant %#v", caseIndex, recased, want)
		}
	}
}

// TestMetamorphicMultiValueOrder checks where the order of the repeated values
// of one header is a difference. The values of a compared header are an
// ordered list (RFC 9110 section 5.3) compared with slices.Equal
// (internal/compare/compare.go:88), while the Set-Cookie headers are compared
// as a set of cookie names (internal/compare/compare.go:172-174).
func TestMetamorphicMultiValueOrder(t *testing.T) {
	t.Run("compared header order is a difference", func(t *testing.T) {
		oldResult := result.Result{StatusCode: 200, Headers: []result.Header{
			{Name: "Access-Control-Allow-Methods", Values: []string{"GET", "POST"}},
		}}
		newResult := result.Result{StatusCode: 200, Headers: []result.Header{
			{Name: "Access-Control-Allow-Methods", Values: []string{"POST", "GET"}},
		}}
		differences := Results("/multi", oldResult, newResult, nil, BodyPolicyHash)
		if len(differences) != 1 || differences[0].Field != "header.access-control-allow-methods" {
			t.Fatalf("differences = %#v", differences)
		}
	})
	t.Run("cookie order is not a difference", func(t *testing.T) {
		for caseIndex := range 50 {
			rnd := metamorphicRand(caseIndex)
			values := randomCookieValues(rnd)
			reversed := make([]string, 0, len(values))
			for i := len(values) - 1; i >= 0; i-- {
				reversed = append(reversed, values[i])
			}
			oldResult := result.Result{StatusCode: 200, Headers: []result.Header{{Name: "Set-Cookie", Values: values}}}
			newResult := result.Result{StatusCode: 200, Headers: []result.Header{{Name: "Set-Cookie", Values: reversed}}}
			differences := Results("/multi", oldResult, newResult, nil, BodyPolicyHash)
			if len(differences) != 0 {
				t.Fatalf("case %d: differences = %#v", caseIndex, differences)
			}
		}
	})
}

// TestMetamorphicIgnoreListMonotonicity checks that adding a header to the
// ignore list never adds a difference and never changes a difference on
// another header (internal/compare/compare.go:94-113). The ignore list is
// case-insensitive (internal/compare/compare.go:77).
func TestMetamorphicIgnoreListMonotonicity(t *testing.T) {
	for caseIndex := range 100 {
		rnd := metamorphicRand(caseIndex)
		oldResult := randomResult(rnd)
		newResult := randomResult(rnd)
		base := Results("/ignore", oldResult, newResult, nil, BodyPolicyHash)
		baseByField := differenceByField(base)
		ignored := make([]string, 0, 5)
		for _, name := range []string{"location", "set-cookie", "strict-transport-security", "x-frame-options", "server"} {
			ignored = append(ignored, strings.ToUpper(name))
			got := Results("/ignore", oldResult, newResult, ignored, BodyPolicyHash)
			if len(got) > len(base) {
				t.Fatalf("case %d: ignoring %q reported %d differences, the base comparison reports %d", caseIndex, name, len(got), len(base))
			}
			for _, difference := range got {
				previous, ok := baseByField[difference.Field]
				if !ok {
					t.Fatalf("case %d: ignoring %q added the difference %#v", caseIndex, name, difference)
				}
				if previous.Code != difference.Code || previous.Old != difference.Old || previous.New != difference.New {
					t.Fatalf("case %d: ignoring %q changed the difference %#v into %#v", caseIndex, name, previous, difference)
				}
			}
		}
	}
}

// TestMetamorphicDeterminism checks that the same input always produces the
// same differences in the same order (CONVENTIONS.md: same input, same output;
// no map iteration in observable output). The differences are sorted by path,
// field and code (internal/compare/compare.go:121-129) and the cookie set is
// rendered from a sorted name list (internal/compare/compare.go:180-184), so
// the Go map iteration order never reaches the output.
func TestMetamorphicDeterminism(t *testing.T) {
	for caseIndex := range 20 {
		rnd := metamorphicRand(caseIndex)
		oldResult := randomResult(rnd)
		newResult := randomResult(rnd)
		want := Results("/determinism", oldResult, newResult, nil, BodyPolicyHash)
		assertSortedDifferences(t, want)
		for range 50 {
			got := Results("/determinism", oldResult, newResult, nil, BodyPolicyHash)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("case %d: the differences changed between calls:\n got %#v\nwant %#v", caseIndex, got, want)
			}
		}
		shuffled := Results("/determinism", shuffleResult(rnd, oldResult), newResult, nil, BodyPolicyHash)
		if !reflect.DeepEqual(shuffled, want) {
			t.Fatalf("case %d: the header order changed the differences:\n got %#v\nwant %#v", caseIndex, shuffled, want)
		}
	}
}

// TestMetamorphicRedactionInvariance checks that a difference whose Location
// query values differ only in the secret is reported on the same fields as a
// redacted one (docs/adr/0006-redaction.md). The comparison uses the raw
// values, so two different secrets still produce a difference, and the report
// layer redacts the two values before serialization while keeping the
// difference.
func TestMetamorphicRedactionInvariance(t *testing.T) {
	oldResult := result.Result{
		StatusCode: 200,
		Headers:    []result.Header{{Name: "Location", Values: []string{"https://api.example.com/cb?code=old-secret"}}},
		BodySHA256: "abc",
	}
	newResult := result.Result{
		StatusCode: 500,
		Headers:    []result.Header{{Name: "Location", Values: []string{"https://api.example.com/cb?code=new-secret"}}},
		BodySHA256: "abc",
	}
	raw := Results("/oauth", oldResult, newResult, nil, BodyPolicyHash)
	if got, want := differenceFields(raw), []string{"header.location", "status"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	redacted := make([]Difference, len(raw))
	for i, difference := range raw {
		redacted[i] = difference
		if difference.Field != "header.location" {
			continue
		}
		redacted[i].Old = redact.URL(difference.Old)
		redacted[i].New = redact.URL(difference.New)
	}
	if got, want := differenceFields(redacted), differenceFields(raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	location := differenceByField(redacted)["header.location"]
	if location.Old != location.New {
		t.Fatalf("redacted values = %q and %q, want them equal", location.Old, location.New)
	}
	if strings.Contains(location.Old, "secret") {
		t.Fatalf("redacted value %q keeps the secret", location.Old)
	}
	// Redaction applied to the observations before the comparison hides the
	// secret, so only the location difference disappears and every other
	// difference is unchanged. The raw comparison is intentional
	// (docs/adr/0006-redaction.md: "Comparison operates on the raw captured
	// Location values so two different secrets still produce a difference").
	preRedacted := Results("/oauth", redactLocation(oldResult), redactLocation(newResult), nil, BodyPolicyHash)
	if got, want := differenceFields(preRedacted), []string{"status"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	rawStatus := differenceByField(raw)["status"]
	preStatus := differenceByField(preRedacted)["status"]
	if rawStatus.Old != preStatus.Old || rawStatus.New != preStatus.New {
		t.Fatalf("status difference = %#v, want %#v", preStatus, rawStatus)
	}
}
