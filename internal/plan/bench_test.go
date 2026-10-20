package plan

import (
	"fmt"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/ingress"
)

// benchIngresses builds count synthetic Ingress resources in memory. The
// resources are deterministic: the same count always yields the same hosts,
// paths and plan, so a benchmark run stays comparable with the previous one.
func benchIngresses(count int) []ingress.Ingress {
	resources := make([]ingress.Ingress, 0, count)
	for index := 0; index < count; index++ {
		name := fmt.Sprintf("app-%04d", index)
		resources = append(resources, ingress.Ingress{
			APIVersion: ingress.APIVersion,
			Kind:       ingress.KindIngress,
			Namespace:  "bench",
			Name:       name,
			ClassName:  "nginx",
			Rules: []ingress.Rule{{
				Host: name + ".example.com",
				Paths: []ingress.Path{
					{Path: "/" + name, PathType: "Prefix"},
					{Path: "/" + name + "/health", PathType: "Exact"},
				},
			}},
		})
	}
	return resources
}

// BenchmarkBuild measures plan generation for 10, 100 and 1000 Ingress
// resources. Plan generation is the hot path of `varco plan`: it runs before
// every comparison, so a regression here delays every run.
func BenchmarkBuild(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("ingress-%d", count), func(b *testing.B) {
			resources := benchIngresses(count)
			options := Options{Profile: ProfileNginx}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				result := Build(resources, options)
				if len(result.Cases) == 0 {
					b.Fatal("plan produced no cases")
				}
			}
		})
	}
}
