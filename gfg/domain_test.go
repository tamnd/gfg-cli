package gfg

import (
	"testing"

	"github.com/tamnd/any-cli/kit"
)

// These tests are offline: they exercise the URI driver's pure string functions
// and the host wiring, which need no network.

func TestDomainInfo(t *testing.T) {
	info := Domain{}.Info()
	if info.Scheme != "gfg" {
		t.Errorf("Scheme = %q, want gfg", info.Scheme)
	}
	if len(info.Hosts) == 0 || info.Hosts[0] != Host {
		t.Errorf("Hosts = %v, want [%s]", info.Hosts, Host)
	}
	if info.Identity.Binary != "gfg" {
		t.Errorf("Identity.Binary = %q, want gfg", info.Identity.Binary)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct{ in, typ, id string }{
		{"dsa/binary-search", "article", "dsa/binary-search"},
		{"/dsa/binary-search/", "article", "dsa/binary-search"},
		{"https://" + Host + "/dsa/binary-search/", "article", "dsa/binary-search"},
		{"python/lists", "article", "python/lists"},
		{"algorithms", "article", "algorithms"},
	}
	for _, tc := range cases {
		typ, id, err := Domain{}.Classify(tc.in)
		if err != nil || typ != tc.typ || id != tc.id {
			t.Errorf("Classify(%q) = (%q, %q, %v), want (%q, %q, nil)",
				tc.in, typ, id, err, tc.typ, tc.id)
		}
	}
}

func TestLocate(t *testing.T) {
	got, err := Domain{}.Locate("article", "dsa/binary-search")
	want := BaseURL + "/dsa/binary-search/"
	if err != nil || got != want {
		t.Errorf("Locate = (%q, %v), want (%q, nil)", got, err, want)
	}

	_, err = Domain{}.Locate("unknown", "foo")
	if err == nil {
		t.Error("Locate(unknown) should return error")
	}

	_, err = Domain{}.Locate("article", "")
	if err == nil {
		t.Error("Locate(article, '') should return error")
	}
}

func TestDomainRegistered(t *testing.T) {
	h, err := kit.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.Domain("gfg"); !ok {
		t.Fatal("gfg domain not registered")
	}
}
