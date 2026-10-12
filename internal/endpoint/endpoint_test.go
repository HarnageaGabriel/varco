package endpoint

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name, input, want string
		port              uint16
		wantErr           bool
	}{
		{name: "IPv4 default", input: "127.0.0.1", port: 443, want: "127.0.0.1:443"},
		{name: "IPv4 port", input: "127.0.0.1:8443", port: 443, want: "127.0.0.1:8443"},
		{name: "IPv6 default", input: "2001:db8::1", port: 443, want: "[2001:db8::1]:443"},
		{name: "IPv6 port", input: "[2001:db8::1]:8443", port: 443, want: "[2001:db8::1]:8443"},
		{name: "hostname", input: "example.com", port: 443, wantErr: true},
		{name: "hostname port", input: "example.com:443", port: 443, wantErr: true},
		{name: "zero port", input: "127.0.0.1:0", port: 443, wantErr: true},
		{name: "empty", port: 443, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.input, test.port)
			if (err != nil) != test.wantErr {
				t.Fatalf("Parse() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && got.String() != test.want {
				t.Fatalf("Parse().String() = %q, want %q", got.String(), test.want)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{"127.0.0.1", "127.0.0.1:443", "2001:db8::1", "[::1]:443", "example.com"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		got, err := Parse(value, 443)
		if err == nil {
			if got.Addr.String() == "" || got.Port == 0 {
				t.Fatalf("invalid successful parse: %#v", got)
			}
			if _, err := Parse(got.String(), 80); err != nil {
				t.Fatalf("round trip failed: %v", err)
			}
		}
	})
}
