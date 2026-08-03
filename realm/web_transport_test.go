package realm

import (
	"testing"

	ma "github.com/multiformats/go-multiaddr"
)

func TestParseRealmWebMultiaddr(t *testing.T) {
	cases := []struct {
		addr       string
		wantHost   string
		wantPort   string
		wantSecure bool
	}{
		{"/dns4/example.com/realm-https/443", "example.com", "443", true},
		{"/ip4/1.2.3.4/realm-http/8080", "1.2.3.4", "8080", false},
	}
	for _, c := range cases {
		a, err := ma.NewMultiaddr(c.addr)
		if err != nil {
			t.Fatalf("%s: %v", c.addr, err)
		}
		host, port, secure, err := parseRealmWebMultiaddr(a)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.addr, err)
		}
		if host != c.wantHost || port != c.wantPort || secure != c.wantSecure {
			t.Fatalf("%s: got (%q, %q, %v), want (%q, %q, %v)", c.addr, host, port, secure, c.wantHost, c.wantPort, c.wantSecure)
		}
	}

	other, err := ma.NewMultiaddr("/ip4/1.2.3.4/tcp/1234")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := parseRealmWebMultiaddr(other); err == nil {
		t.Fatalf("expected error for a non-realm-web multiaddr")
	}
}

func TestWebTransportCanDial(t *testing.T) {
	tr, err := newWebTransport(nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	yes, err := ma.NewMultiaddr("/dns4/example.com/realm-https/443")
	if err != nil {
		t.Fatal(err)
	}
	if !tr.CanDial(yes) {
		t.Fatalf("expected CanDial(%s) to be true", yes)
	}

	no, err := ma.NewMultiaddr("/ip4/1.2.3.4/tcp/1234")
	if err != nil {
		t.Fatal(err)
	}
	if tr.CanDial(no) {
		t.Fatalf("expected CanDial(%s) to be false", no)
	}
}
