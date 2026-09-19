package controller

import "testing"

func TestScaleUpBreach(t *testing.T) {
	cfg := DefaultPolicyConfig()

	cases := []struct {
		name     string
		signal   Signals
		expected bool
	}{
		{
			name:     "todo comodo, no debe alertar",
			signal:   Signals{CPUUtilization: 10, P95LatencyMillis: 150, RequestsPerTarget: 2},
			expected: false,
		},
		{
			name:     "CPU sola supera el umbral",
			signal:   Signals{CPUUtilization: 50, P95LatencyMillis: 150, RequestsPerTarget: 2},
			expected: true,
		},
		{
			name:     "latencia sola supera el umbral",
			signal:   Signals{CPUUtilization: 10, P95LatencyMillis: 1200, RequestsPerTarget: 2},
			expected: true,
		},
		{
			name:     "muchos requests pero CPU baja, no debe alertar",
			signal:   Signals{CPUUtilization: 10, P95LatencyMillis: 150, RequestsPerTarget: 20},
			expected: false,
		},
		{
			name:     "muchos requests con CPU moderada, si debe alertar",
			signal:   Signals{CPUUtilization: 35, P95LatencyMillis: 150, RequestsPerTarget: 20},
			expected: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scaleUpBreach(c.signal, cfg)
			if got != c.expected {
				t.Errorf("scaleUpBreach(%+v) = %v, esperado %v", c.signal, got, c.expected)
			}
		})
	}
}