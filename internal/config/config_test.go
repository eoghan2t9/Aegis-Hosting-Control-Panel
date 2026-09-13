package config

import "testing"

func TestNormalizePanelBase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""}, {"/", ""}, {"-", ""},
		{"aegis", "/aegis"},
		{"/aegis", "/aegis"},
		{"/aegis/", "/aegis"},
		{"  /panel  ", "/panel"},
	}
	for _, c := range cases {
		if got := normalizePanelBase(c.in); got != c.want {
			t.Errorf("normalizePanelBase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPanelUpstream(t *testing.T) {
	cases := []struct{ listen, want string }{
		{":8080", "127.0.0.1:8080"},
		{"0.0.0.0:8080", "127.0.0.1:8080"},
		{"127.0.0.1:8080", "127.0.0.1:8080"},
		{"10.0.0.5:9000", "10.0.0.5:9000"},
	}
	for _, c := range cases {
		cfg := Default()
		cfg.ListenAddr = c.listen
		if got := cfg.PanelUpstream(); got != c.want {
			t.Errorf("PanelUpstream(%q) = %q, want %q", c.listen, got, c.want)
		}
	}
}
