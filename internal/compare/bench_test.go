package compare

import (
	"fmt"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/result"
)

// benchResult builds one observed response with count synthetic headers plus
// the headers varco compares. The two sides differ in status and body, so the
// comparison always has real work to do.
func benchResult(count int, status int, body string) result.Result {
	headers := make([]result.Header, 0, count+2)
	for index := 0; index < count; index++ {
		headers = append(headers, result.Header{
			Name:   fmt.Sprintf("X-Bench-%04d", index),
			Values: []string{fmt.Sprintf("value-%04d", index)},
		})
	}
	headers = append(headers,
		result.Header{Name: "Content-Security-Policy", Values: []string{"default-src 'self'"}},
		result.Header{Name: "Set-Cookie", Values: []string{"session=bench; Secure; HttpOnly; Path=/; SameSite=Lax"}},
	)
	return result.Result{
		StatusCode: status,
		Headers:    headers,
		TLSVersion: "TLS 1.3",
		BodySHA256: body,
		BodySize:   int64(len(body)),
	}
}

// BenchmarkCompareResults measures the comparison of two responses with a
// small and a large header set. Comparison runs once per probed case, so its
// cost is multiplied by the size of the plan.
func BenchmarkCompareResults(b *testing.B) {
	for _, test := range []struct {
		name  string
		count int
	}{
		{name: "small-headers", count: 4},
		{name: "large-headers", count: 256},
	} {
		b.Run(test.name, func(b *testing.B) {
			oldResult := benchResult(test.count, 200, "aaaa")
			newResult := benchResult(test.count, 404, "bbbb")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				differences := Results("/bench", oldResult, newResult, nil, BodyPolicyHash)
				if len(differences) == 0 {
					b.Fatal("comparison produced no differences")
				}
			}
		})
	}
}
