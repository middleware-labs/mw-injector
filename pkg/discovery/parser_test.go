package discovery_test

import (
	"reflect"
	"testing"

	"github.com/middleware-labs/java-injector/pkg/discovery/internal"
)

func TestParseCommandLine(t *testing.T) {
	tests := []struct {
		name     string
		cmdline  string
		expected []string
	}{
		{
			name:     "Simple command",
			cmdline:  "java -jar app.jar",
			expected: []string{"java", "-jar", "app.jar"},
		},
		{
			name:     "Command with quoted arguments",
			cmdline:  `java -Dfile.path="/path with spaces/file.txt" -jar app.jar`,
			expected: []string{"java", "-Dfile.path=/path with spaces/file.txt", "-jar", "app.jar"},
		},
		{
			name:     "Null-separated arguments",
			cmdline:  "java\x00-jar\x00app.jar\x00",
			expected: []string{"java", "-jar", "app.jar"},
		},
		{
			name:     "Complex Java command",
			cmdline:  `java -Xmx512m -Dspring.profiles.active=prod -javaagent:agent.jar -jar /opt/app/service.jar --server.port=8080`,
			expected: []string{"java", "-Xmx512m", "-Dspring.profiles.active=prod", "-javaagent:agent.jar", "-jar", "/opt/app/service.jar", "--server.port=8080"},
		},
		{
			name:     "Empty command line",
			cmdline:  "",
			expected: []string{},
		},
		{
			name:     "Command with escaped quotes",
			cmdline:  `java -Darg="value with \"quotes\""`,
			expected: []string{"java", `-Darg=value with "quotes"`},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := internal.ParseCommandLine(test.cmdline)
			if !reflect.DeepEqual(result, test.expected) {
				t.Errorf("ParseCommandLine(%q) = %v, expected %v", test.cmdline, result, test.expected)
			}
		})
	}
}

func TestExtractJarFiles(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "Single JAR file",
			args:     []string{"java", "-jar", "app.jar"},
			expected: []string{"app.jar"},
		},
		{
			name:     "Multiple JAR files",
			args:     []string{"java", "-cp", "lib1.jar:lib2.jar", "-jar", "app.jar"},
			expected: []string{"lib1.jar:lib2.jar", "app.jar"},
		},
		{
			name:     "No JAR files",
			args:     []string{"java", "com.example.Main"},
			expected: nil,
		},
		{
			name:     "JAR with path",
			args:     []string{"java", "-jar", "/opt/services/microservice-auth.jar"},
			expected: []string{"/opt/services/microservice-auth.jar"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := internal.ExtractJarFiles(test.args)
			if !reflect.DeepEqual(result, test.expected) {
				t.Errorf("ExtractJarFiles(%v) = %v, expected %v", test.args, result, test.expected)
			}
		})
	}
}

func TestExtractSystemProperties(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected map[string]string
	}{
		{
			name: "Basic system properties",
			args: []string{"java", "-Dspring.profiles.active=prod", "-Dserver.port=8080", "-jar", "app.jar"},
			expected: map[string]string{
				"spring.profiles.active": "prod",
				"server.port":            "8080",
			},
		},
		{
			name: "Property without value",
			args: []string{"java", "-Ddebug", "-Dverbose", "-jar", "app.jar"},
			expected: map[string]string{
				"debug":   "true",
				"verbose": "true",
			},
		},
		{
			name:     "No system properties",
			args:     []string{"java", "-jar", "app.jar"},
			expected: map[string]string{},
		},
		{
			name: "OTEL service name",
			args: []string{"java", "-Dotel.service.name=my-service", "-jar", "app.jar"},
			expected: map[string]string{
				"otel.service.name": "my-service",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := internal.ExtractSystemProperties(test.args)
			if !reflect.DeepEqual(result, test.expected) {
				t.Errorf("ExtractSystemProperties(%v) = %v, expected %v", test.args, result, test.expected)
			}
		})
	}
}

func TestExtractJVMOptions(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "Common JVM options",
			args:     []string{"java", "-Xmx512m", "-Xms256m", "-XX:+UseG1GC", "-jar", "app.jar"},
			expected: []string{"-Xmx512m", "-Xms256m", "-XX:+UseG1GC"},
		},
		{
			name:     "Mixed options and arguments",
			args:     []string{"java", "-server", "-Dspring.profiles.active=prod", "-javaagent:agent.jar", "-jar", "app.jar", "--port=8080"},
			expected: []string{"-server", "-Dspring.profiles.active=prod", "-javaagent:agent.jar"},
		},
		{
			name:     "Assertion options",
			args:     []string{"java", "-ea", "-da:com.untrusted...", "-jar", "app.jar"},
			expected: []string{"-ea", "-da:com.untrusted..."},
		},
		{
			name:     "No JVM options",
			args:     []string{"java", "com.example.Main"},
			expected: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := internal.ExtractJVMOptions(test.args)
			if !reflect.DeepEqual(result, test.expected) {
				t.Errorf("ExtractJVMOptions(%v) = %v, expected %v", test.args, result, test.expected)
			}
		})
	}
}

func TestFindMainClass(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "Main class with package",
			args:     []string{"java", "com.example.Application"},
			expected: "com.example.Application",
		},
		{
			name:     "JAR execution",
			args:     []string{"java", "-jar", "app.jar"},
			expected: "",
		},
		{
			name:     "Main class with JVM options",
			args:     []string{"java", "-Xmx512m", "-Dspring.profiles.active=prod", "com.example.Main"},
			expected: "com.example.Main",
		},
		{
			name:     "Spring Boot main class",
			args:     []string{"java", "org.springframework.boot.loader.JarLauncher"},
			expected: "org.springframework.boot.loader.JarLauncher",
		},
		{
			name:     "No main class",
			args:     []string{"java", "-version"},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := internal.FindMainClass(test.args)
			if result != test.expected {
				t.Errorf("FindMainClass(%v) = %q, expected %q", test.args, result, test.expected)
			}
		})
	}
}

func TestExtractSpringBootInfo(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected map[string]string
	}{
		{
			name: "Spring Boot properties",
			args: []string{
				"java",
				"-Dspring.application.name=user-service",
				"-Dspring.profiles.active=production,monitoring",
				"-Dserver.port=8080",
				"-jar", "app.jar",
			},
			expected: map[string]string{
				"spring.application.name": "user-service",
				"spring.profiles.active":  "production,monitoring",
				"server.port":             "8080",
			},
		},
		{
			name:     "No Spring Boot properties",
			args:     []string{"java", "-jar", "app.jar"},
			expected: map[string]string{},
		},
		{
			name: "Partial Spring Boot properties",
			args: []string{
				"java",
				"-Dspring.application.name=auth-service",
				"-Dother.property=value",
				"-jar", "app.jar",
			},
			expected: map[string]string{
				"spring.application.name": "auth-service",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := internal.ExtractSpringBootInfo(test.args)
			if !reflect.DeepEqual(result, test.expected) {
				t.Errorf("ExtractSpringBootInfo(%v) = %v, expected %v", test.args, result, test.expected)
			}
		})
	}
}

func TestNormalizeServiceName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple name",
			input:    "UserService",
			expected: "userservice",
		},
		{
			name:     "Name with underscores",
			input:    "user_auth_service",
			expected: "user-auth-service",
		},
		{
			name:     "Name with spaces",
			input:    "User Auth Service",
			expected: "user-auth-service",
		},
		{
			name:     "Name with special characters",
			input:    "user@service#api",
			expected: "userserviceapi",
		},
		{
			name:     "Name with multiple hyphens",
			input:    "user---service",
			expected: "user-service",
		},
		{
			name:     "Name with leading/trailing hyphens",
			input:    "-user-service-",
			expected: "user-service",
		},
		{
			name:     "Empty name",
			input:    "",
			expected: "",
		},
		{
			name:     "Version in name",
			input:    "microservice-auth-v1.2.3",
			expected: "microservice-auth-v1-2-3",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := internal.NormalizeServiceName(test.input)
			if result != test.expected {
				t.Errorf("NormalizeServiceName(%q) = %q, expected %q", test.input, result, test.expected)
			}
		})
	}
}

func TestIsGenericName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Generic name - app",
			input:    "app",
			expected: true,
		},
		{
			name:     "Generic name - service",
			input:    "service",
			expected: true,
		},
		{
			name:     "Generic name - demo",
			input:    "demo",
			expected: true,
		},
		{
			name:     "Specific name",
			input:    "user-authentication-service",
			expected: false,
		},
		{
			name:     "Mixed case generic",
			input:    "APPLICATION",
			expected: true,
		},
		{
			name:     "Empty name",
			input:    "",
			expected: false,
		},
		{
			name:     "Non-generic name",
			input:    "payment-processor",
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := internal.IsGenericName(test.input)
			if result != test.expected {
				t.Errorf("IsGenericName(%q) = %v, expected %v", test.input, result, test.expected)
			}
		})
	}
}
