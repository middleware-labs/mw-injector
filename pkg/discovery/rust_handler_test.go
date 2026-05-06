package discovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRustHandlerDetect(t *testing.T) {
	tests := []struct {
		name    string
		exeName string
		exePath string
		want    bool
	}{
		{
			name:    "non-rust binary",
			exeName: "python3",
			exePath: "/usr/bin/python3",
			want:    false,
		},
		{
			name:    "empty exe path",
			exeName: "",
			exePath: "",
			want:    false,
		},
		{
			name:    "nonexistent path",
			exeName: "myapp",
			exePath: "/nonexistent/path/to/binary",
			want:    false,
		},
	}

	h := &RustHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &ProcessInfo{
				PID:     12345,
				ExeName: tt.exeName,
				ExePath: tt.exePath,
			}
			got := h.Detect(proc)
			if got != tt.want {
				t.Errorf("Detect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRustHandlerDetectRealBinary(t *testing.T) {
	rustc, err := exec.LookPath("rustc")
	if err != nil {
		t.Skip("rustc not available, skipping real binary test")
	}

	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "main.rs")
	if err := os.WriteFile(src, []byte("fn main() { println!(\"hello\"); }"), 0644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(tmpDir, "hello")
	cmd := exec.Command(rustc, src, "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("rustc compilation failed: %v\n%s", err, out)
	}

	h := &RustHandler{}
	proc := &ProcessInfo{
		PID:     99999,
		ExeName: "hello",
		ExePath: bin,
	}
	if !h.Detect(proc) {
		t.Error("Detect() = false for compiled Rust binary, want true")
	}
}

func TestRustHandlerDetectStrippedBinary(t *testing.T) {
	rustc, err := exec.LookPath("rustc")
	if err != nil {
		t.Skip("rustc not available")
	}
	strip, err := exec.LookPath("strip")
	if err != nil {
		t.Skip("strip not available")
	}

	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "main.rs")
	if err := os.WriteFile(src, []byte("fn main() {}"), 0644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(tmpDir, "stripped")
	cmd := exec.Command(rustc, src, "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("rustc compilation failed: %v\n%s", err, out)
	}

	// Strip symbols but .comment survives
	cmd = exec.Command(strip, "--strip-all", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("strip failed: %v\n%s", err, out)
	}

	h := &RustHandler{}
	proc := &ProcessInfo{
		PID:     99999,
		ExeName: "stripped",
		ExePath: bin,
	}
	if !h.Detect(proc) {
		t.Error("Detect() = false for stripped Rust binary, want true (.comment should survive)")
	}
}

func TestRustHandlerExtractServiceName(t *testing.T) {
	tests := []struct {
		name        string
		exeName     string
		exePath     string
		wantService string
	}{
		{
			name:        "normal rust binary name",
			exeName:     "my-web-server",
			exePath:     "/usr/local/bin/my-web-server",
			wantService: "my-web-server",
		},
		{
			name:        "generic name falls to working dir",
			exeName:     "server",
			exePath:     "/opt/myapp/server",
			wantService: "rust-service",
		},
		{
			name:        "binary with underscores",
			exeName:     "payment_processor",
			exePath:     "/usr/bin/payment_processor",
			wantService: "payment-processor",
		},
	}

	h := &RustHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &Process{
				PID:            99999,
				ExecutablePath: tt.exePath,
				Language:       LangRust,
				Details:        make(map[string]any),
			}
			info := &ProcessInfo{
				PID:     99999,
				ExeName: tt.exeName,
				ExePath: tt.exePath,
			}

			h.extractServiceName(proc, info)

			if proc.ServiceName != tt.wantService {
				t.Errorf("ServiceName = %q, want %q", proc.ServiceName, tt.wantService)
			}
		})
	}
}

func TestRustHandlerLang(t *testing.T) {
	h := &RustHandler{}
	if h.Lang() != LangRust {
		t.Errorf("Lang() = %q, want %q", h.Lang(), LangRust)
	}
}

func TestIsRustBinaryFileSize(t *testing.T) {
	tmpDir := t.TempDir()
	largePath := filepath.Join(tmpDir, "large")

	// Create a file just over the limit — should be skipped
	f, err := os.Create(largePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(rustMaxBinarySize + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	if isRustBinary(0, largePath) {
		t.Error("isRustBinary() = true for oversized file, want false")
	}
}

func TestHasRustComment(t *testing.T) {
	rustc, err := exec.LookPath("rustc")
	if err != nil {
		t.Skip("rustc not available")
	}

	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "main.rs")
	if err := os.WriteFile(src, []byte("fn main() {}"), 0644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(tmpDir, "testbin")
	cmd := exec.Command(rustc, src, "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("rustc compilation failed: %v\n%s", err, out)
	}

	if !isRustBinary(0, bin) {
		t.Error("isRustBinary() = false for real Rust binary")
	}
}

func TestStdRustcVersionRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"stable", "rustc version 1.91.0 (f8297e351 2025-10-28)", true},
		{"beta", "rustc version 1.92.0-beta.1 (abc123def 2025-11-01)", true},
		{"beta bare", "rustc version 1.92.0-beta (abc123def 2025-11-01)", true},
		{"nightly", "rustc version 1.93.0-nightly (def456abc 2025-11-15)", true},
		{"chromium vendor fork", "rustc version 1.92.0-dev (15283f6fe95e5b604273d13a428bab5fc0788f5a-1-llvmorg-22-init-8940-g4d4cb757 chromium)", false},
		{"dev no date", "rustc version 1.42.0-dev", false},
		{"garbage", "not a rustc string", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stdRustcVersion.MatchString(tt.input)
			if got != tt.want {
				t.Errorf("stdRustcVersion.Match(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestCBinaryNotDetectedAsRust(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not available")
	}

	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(src, []byte("int main() { return 0; }"), 0644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(tmpDir, "cbin")
	cmd := exec.Command(gcc, src, "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("gcc compilation failed: %v\n%s", err, out)
	}

	if isRustBinary(0, bin) {
		t.Error("isRustBinary() = true for C binary, want false")
	}
}
