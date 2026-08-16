package config

import "testing"

func TestFromArgsResolvesBoundedConcurrency(t *testing.T) {
	config, err := FromArgs([]string{"--parallel", "4", "--max-queue", "12"})
	if err != nil {
		t.Fatalf("FromArgs() error = %v", err)
	}
	if config.Parallel != 4 || config.MaxQueue != 12 {
		t.Fatalf("concurrency config = parallel %d queue %d", config.Parallel, config.MaxQueue)
	}
}

func TestFromArgsRejectsInvalidResourceBounds(t *testing.T) {
	for _, arguments := range [][]string{
		{"--ctx", "0"},
		{"--parallel", "0"},
		{"--max-queue", "-1"},
		{"--max-tokens", "-1"},
		{"--max-gen-time", "0s"},
		{"--ctx", "1048577"},
		{"--parallel", "1025"},
		{"--max-queue", "65537"},
		{"--max-tokens", "1048577"},
		{"--temp", "-0.1"},
		{"--temp", "2.1"},
	} {
		if _, err := FromArgs(arguments); err == nil {
			t.Fatalf("FromArgs(%v) accepted invalid resource bounds", arguments)
		}
	}
}

func TestFromArgsRejectsMalformedExplicitEnvironment(t *testing.T) {
	for _, test := range []struct {
		key   string
		value string
	}{
		{key: "PLLM_CTX", value: "many"},
		{key: "PLLM_TEMP", value: "warm"},
		{key: "PLLM_MAX_GEN_TIME", value: "soon"},
		{key: "PLLM_TOKEN_AUTH", value: "maybe"},
	} {
		t.Run(test.key, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if _, err := FromArgs(nil); err == nil {
				t.Fatalf("FromArgs() accepted %s=%q", test.key, test.value)
			}
		})
	}
}

func TestFromArgsRejectsNonFiniteTemperature(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "-Inf"} {
		if _, err := FromArgs([]string{"--temp", value}); err == nil {
			t.Fatalf("FromArgs(--temp %s) accepted a non-finite value", value)
		}
	}
}
