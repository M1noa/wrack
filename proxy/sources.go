package proxy

import (
	_ "embed"
	"encoding/json"
)

//go:embed sources.json
var embeddedSources []byte

// Source is a single proxy list URL.
type Source struct {
	URL    string `json:"url"`
	Format string `json:"format"`
	Parser string `json:"parser"`
}

// Sources groups scrape URLs by proxy protocol.
type Sources struct {
	HTTP   []Source `json:"http"`
	SOCKS4 []Source `json:"socks4"`
	SOCKS5 []Source `json:"socks5"`
}

// LoadEmbedded parses the embedded sources.json (originally sourced from
// bombers/bomber/sources.hjson; content is valid JSON).
func LoadEmbedded() (*Sources, error) {
	var s Sources
	if err := json.Unmarshal(embeddedSources, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Flat returns every source with its protocol tag attached.
func (s *Sources) Flat() []TaggedSource {
	out := make([]TaggedSource, 0, len(s.HTTP)+len(s.SOCKS4)+len(s.SOCKS5))
	for _, v := range s.HTTP {
		out = append(out, TaggedSource{Protocol: "http", Source: v})
	}
	for _, v := range s.SOCKS4 {
		out = append(out, TaggedSource{Protocol: "socks4", Source: v})
	}
	for _, v := range s.SOCKS5 {
		out = append(out, TaggedSource{Protocol: "socks5", Source: v})
	}
	return out
}

// TaggedSource pairs a Source with its proxy protocol.
type TaggedSource struct {
	Protocol string
	Source   Source
}
