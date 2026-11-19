package tasks

import (
	"reflect"
	"testing"
)

func TestParseHopsSkipsHeader(t *testing.T) {
	text := `traceroute to sh-ct-v4.ip.zstaticcdn.com (101.226.101.195), 25 hops max, 60 byte packets
 1  172.30.208.1  0.250 ms
 2  192.168.1.1  0.386 ms
 3  *
 4  202.97.120.213  25.302 ms
10  101.95.219.70  27.659 ms
`
	got := parseHops(text)
	want := []string{"172.30.208.1", "192.168.1.1", "202.97.120.213", "101.95.219.70"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestGeminiVerdict(t *testing.T) {
	cases := []struct{ body, status, region string }{
		{`x,2,1,200,"USA"y "45631641\":true,\"45841483\":true`, "yes", "US"},
		{`x,2,1,200,"JPN"y [45631641,null,true]`, "yes", "JP"},
		{`x,2,1,200,"HKG"y "45631641\":false`, "no", "HK"},
		{`x,2,1,200,"CHN"y no flag here`, "no", "CN"},
		{`x,2,1,200,"DEU"y no flag here`, "yes", "DE"},
		{`unrelated page`, "unknown", ""},
	}
	for _, c := range cases {
		s, r, _ := geminiVerdict(c.body)
		if s != c.status || r != c.region {
			t.Errorf("%q: got %s/%s want %s/%s", c.body, s, r, c.status, c.region)
		}
	}
}
