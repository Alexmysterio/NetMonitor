package poller

import (
	"math"
	"testing"
)

func TestAggregateLogicAll(t *testing.T) {
	res := aggregateLogic([]string{"Ping", "CPU", "HTTP"}, []string{"up", "up", "warning"}, "all")
	if !res.OK || !res.Warning || !res.HasValue || math.Abs(res.Value-66.67) > 0.01 {
		t.Fatalf("unexpected warning result: %+v", res)
	}
	res = aggregateLogic([]string{"Ping", "HTTP"}, []string{"up", "down"}, "all")
	if res.OK || res.Message == "" {
		t.Fatalf("down child must fail AND mode: %+v", res)
	}
	res = aggregateLogic([]string{"Ping", "HTTP"}, []string{"up", "up"}, "all")
	if !res.OK || res.Warning || res.Value != 100 {
		t.Fatalf("unexpected healthy result: %+v", res)
	}
}

func TestAggregateLogicAny(t *testing.T) {
	res := aggregateLogic([]string{"Ping", "HTTP"}, []string{"down", "up"}, "any")
	if !res.OK || res.Message == "" {
		t.Fatalf("one healthy child must pass OR mode: %+v", res)
	}
	res = aggregateLogic([]string{"Ping", "HTTP"}, []string{"down", "unreachable"}, "any")
	if res.OK {
		t.Fatalf("no healthy child must fail OR mode: %+v", res)
	}
}
