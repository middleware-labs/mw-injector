package discovery

import "testing"

func TestExtractPythonInfo(t *testing.T) {
	tests := []struct {
		name           string
		cmdArgs        []string
		wantEntryPoint string
		wantModulePath string
	}{
		{
			name:           "module invocation with -m",
			cmdArgs:        []string{"/usr/bin/python3", "-m", "flask", "run"},
			wantEntryPoint: "flask",
			wantModulePath: "flask",
		},
		{
			name:           "script with .py extension",
			cmdArgs:        []string{"/usr/bin/python3", "app.py"},
			wantEntryPoint: "app.py",
			wantModulePath: "",
		},
		{
			name:           "uvicorn direct invocation",
			cmdArgs:        []string{"/usr/bin/python3", "uvicorn", "main:app", "--host", "0.0.0.0"},
			wantEntryPoint: "main:app",
			wantModulePath: "",
		},
		{
			name:           "gunicorn with module spec",
			cmdArgs:        []string{"/usr/bin/python3", "gunicorn", "myapp:app", "-w", "4"},
			wantEntryPoint: "myapp:app",
			wantModulePath: "",
		},
		{
			name:           "non-py script (system utility)",
			cmdArgs:        []string{"/usr/bin/python3", "/usr/share/unattended-upgrades/unattended-upgrade-shutdown", "--wait-for-signal"},
			wantEntryPoint: "unattended-upgrade-shutdown",
			wantModulePath: "",
		},
		{
			name:           "celery worker",
			cmdArgs:        []string{"/usr/bin/python3", "celery", "worker", "--app=myapp"},
			wantEntryPoint: "worker",
			wantModulePath: "",
		},
		{
			name:           "plain python with no args",
			cmdArgs:        []string{"/usr/bin/python3"},
			wantEntryPoint: "",
			wantModulePath: "",
		},
		{
			name:           "python with only flags",
			cmdArgs:        []string{"/usr/bin/python3", "-u", "-O"},
			wantEntryPoint: "",
			wantModulePath: "",
		},
		{
			name:           "absolute path py script",
			cmdArgs:        []string{"/usr/bin/python3", "/opt/myapp/run.py"},
			wantEntryPoint: "run.py",
			wantModulePath: "",
		},
		{
			name:           "wsgi notation without tool prefix",
			cmdArgs:        []string{"/usr/bin/python3", "main:app"},
			wantEntryPoint: "main:app",
			wantModulePath: "",
		},
	}

	h := &PythonHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &Process{
				PID:            99999,
				ExecutablePath: "/usr/bin/python3",
				Language:       LangPython,
				Details:        make(map[string]any),
			}

			h.extractPythonInfo(proc, tt.cmdArgs)

			gotEP := proc.DetailString(DetailEntryPoint)
			if gotEP != tt.wantEntryPoint {
				t.Errorf("DetailEntryPoint = %q, want %q", gotEP, tt.wantEntryPoint)
			}

			gotMP := proc.DetailString(DetailModulePath)
			if gotMP != tt.wantModulePath {
				t.Errorf("DetailModulePath = %q, want %q", gotMP, tt.wantModulePath)
			}
		})
	}
}
