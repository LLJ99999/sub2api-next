package usagestats

import (
	"math"
	"testing"
)

func i64(v int64) *int64       { return &v }
func f64(v float64) *float64   { return &v }
func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCacheHitRate(t *testing.T) {
	tests := []struct {
		name                string
		input, read, create int64
		want                float64
		wantOK              bool
	}{
		{name: "anthropic style exclusive buckets", input: 40_000, read: 278_272, create: 1_728, want: 278_272.0 / 320_000.0, wantOK: true},
		// OpenAI prompt_tokens=10000 含 cached_tokens=8000：落库时 input_tokens 已是 2000。
		{name: "openai normalized on write", input: 2_000, read: 8_000, create: 0, want: 0.8, wantOK: true},
		{name: "cache write stays in denominator", input: 100, read: 100, create: 100, want: 1.0 / 3.0, wantOK: true},
		{name: "no hit", input: 1_200, want: 0, wantOK: true},
		{name: "all from cache", read: 500, want: 1, wantOK: true},
		{name: "no prompt tokens", wantOK: false},
		{name: "negative bucket", input: -1, read: 10, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := CacheHitRate(tc.input, tc.read, tc.create)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && !approx(got, tc.want) {
				t.Fatalf("rate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOutputTPS(t *testing.T) {
	tests := []struct {
		name   string
		in     OutputTPSInput
		want   float64
		wantOK bool
	}{
		{name: "generation window excludes first token", in: OutputTPSInput{OutputTokens: 872, DurationMs: i64(31_260), FirstTokenMs: i64(2_910)}, want: 872.0 / 28.35, wantOK: true},
		{name: "missing first token uses whole duration", in: OutputTPSInput{OutputTokens: 500, DurationMs: i64(10_000)}, want: 50, wantOK: true},
		{name: "exactly min output tokens", in: OutputTPSInput{OutputTokens: OutputTPSMinOutputTokens, DurationMs: i64(2_000), FirstTokenMs: i64(1_000)}, want: 20, wantOK: true},
		{name: "below min output tokens", in: OutputTPSInput{OutputTokens: OutputTPSMinOutputTokens - 1, DurationMs: i64(2_000), FirstTokenMs: i64(1_000)}, wantOK: false},
		{name: "zero generation window", in: OutputTPSInput{OutputTokens: 500, DurationMs: i64(10_000), FirstTokenMs: i64(10_000)}, wantOK: false},
		{name: "negative generation window", in: OutputTPSInput{OutputTokens: 500, DurationMs: i64(10_000), FirstTokenMs: i64(10_001)}, wantOK: false},
		// 回归：整段缓冲输出 10ms 内下发，不得出现 109500 t/s 的尖峰。
		{name: "buffered output spike is hidden", in: OutputTPSInput{OutputTokens: 1_095, DurationMs: i64(9_250), FirstTokenMs: i64(9_240)}, wantOK: false},
		{name: "missing duration", in: OutputTPSInput{OutputTokens: 500}, wantOK: false},
		{name: "media request", in: OutputTPSInput{OutputTokens: 4_160, DurationMs: i64(40_000), IsMedia: true}, wantOK: false},
		{name: "negative first token", in: OutputTPSInput{OutputTokens: 500, DurationMs: i64(10_000), FirstTokenMs: i64(-1)}, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := OutputTPS(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (tps=%v)", ok, tc.wantOK, got)
			}
			if ok && !approx(got, tc.want) {
				t.Fatalf("tps = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUpstreamCostAndMargin(t *testing.T) {
	if got := UpstreamCost(2, nil, nil); !approx(got, 2) {
		t.Fatalf("nil multiplier must count as 1.0, got %v", got)
	}
	if got := UpstreamCost(2, nil, f64(0.5)); !approx(got, 1) {
		t.Fatalf("got %v", got)
	}
	if got := UpstreamCost(2, f64(3), f64(0.5)); !approx(got, 1.5) {
		t.Fatalf("account_stats_cost must override total_cost, got %v", got)
	}

	margin, pct, ok := GrossMargin(10, 4)
	if !ok || !approx(margin, 6) || !approx(pct, 0.6) {
		t.Fatalf("margin=%v pct=%v ok=%v", margin, pct, ok)
	}
	margin, _, ok = GrossMargin(0, 4)
	if ok || !approx(margin, -4) {
		t.Fatalf("zero revenue: margin=%v ok=%v", margin, ok)
	}
}
