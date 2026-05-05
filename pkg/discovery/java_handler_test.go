package discovery

import "testing"

func TestStripJarVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"app-1.0.0.jar", "app"},
		{"app-1.0.1-SNAPSHOT.jar", "app"},
		{"spring-boot-2.7.1-SNAPSHOT.jar", "spring-boot"},
		{"myapp.jar", "myapp"},
		{"my-service_1.2.3.jar", "my-service"},
		{"app_2.0_BUILD_42.jar", "app"},
		{"", ""},
		{"plain", "plain"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripJarVersion(tt.input)
			if got != tt.want {
				t.Errorf("stripJarVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractNameFromJarBackwardCompat(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"app-1.0.0.jar", ""},
		{"spring-boot-2.7.1-SNAPSHOT.jar", "spring-boot"},
		{"myapp.jar", "myapp"},
		{"", ""},
		{"billing-api-3.2.1.jar", "billing-api"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractNameFromJar(tt.input)
			if got != tt.want {
				t.Errorf("extractNameFromJar(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
