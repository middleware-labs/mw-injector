package discovery

import "testing"

func TestJavaInspector(t *testing.T) {
	tests := []struct {
		name     string
		proc     ProcessInfo
		wantLang Language
		wantOK   bool
	}{
		{
			name:     "standard java executable",
			proc:     ProcessInfo{ExeName: "java", CmdLine: "java -jar app.jar"},
			wantLang: LangJava,
			wantOK:   true,
		},
		{
			name:     "java in path-like exe name",
			proc:     ProcessInfo{ExeName: "java", ExePath: "/usr/lib/jvm/java-17/bin/java", CmdLine: "/usr/lib/jvm/java-17/bin/java -jar app.jar"},
			wantLang: LangJava,
			wantOK:   true,
		},
		{
			name:     "not java",
			proc:     ProcessInfo{ExeName: "python3", CmdLine: "python3 app.py"},
			wantLang: "",
			wantOK:   false,
		},
		{
			name:     "java in cmdline only",
			proc:     ProcessInfo{ExeName: "some-wrapper", CmdLine: "java -Xmx512m -jar service.jar"},
			wantLang: LangJava,
			wantOK:   true,
		},
	}

	inspector := &JavaInspector{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang, ok := inspector.Inspect(&tt.proc)
			if ok != tt.wantOK {
				t.Errorf("Inspect() ok = %v, want %v", ok, tt.wantOK)
			}
			if lang != tt.wantLang {
				t.Errorf("Inspect() lang = %q, want %q", lang, tt.wantLang)
			}
		})
	}
}

func TestNodeInspector(t *testing.T) {
	tests := []struct {
		name     string
		proc     ProcessInfo
		wantLang Language
		wantOK   bool
	}{
		{
			name:     "node executable",
			proc:     ProcessInfo{ExeName: "node", CmdLine: "node server.js"},
			wantLang: LangNode,
			wantOK:   true,
		},
		{
			name:     "nodejs executable",
			proc:     ProcessInfo{ExeName: "nodejs", CmdLine: "nodejs app.js"},
			wantLang: LangNode,
			wantOK:   true,
		},
		{
			name:     "npm start pattern",
			proc:     ProcessInfo{ExeName: "npm", CmdLine: "npm start"},
			wantLang: LangNode,
			wantOK:   true,
		},
		{
			name:     "npx pattern",
			proc:     ProcessInfo{ExeName: "npx", CmdLine: "npx ts-node server.ts"},
			wantLang: LangNode,
			wantOK:   true,
		},
		{
			name:     "not node",
			proc:     ProcessInfo{ExeName: "python3", CmdLine: "python3 app.py"},
			wantLang: "",
			wantOK:   false,
		},
	}

	inspector := &NodeInspector{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang, ok := inspector.Inspect(&tt.proc)
			if ok != tt.wantOK {
				t.Errorf("Inspect() ok = %v, want %v", ok, tt.wantOK)
			}
			if lang != tt.wantLang {
				t.Errorf("Inspect() lang = %q, want %q", lang, tt.wantLang)
			}
		})
	}
}

func TestPythonInspector(t *testing.T) {
	tests := []struct {
		name     string
		proc     ProcessInfo
		wantLang Language
		wantOK   bool
	}{
		{
			name:     "python3 executable",
			proc:     ProcessInfo{ExeName: "python3", CmdLine: "python3 app.py"},
			wantLang: LangPython,
			wantOK:   true,
		},
		{
			name:     "python executable",
			proc:     ProcessInfo{ExeName: "python", CmdLine: "python app.py"},
			wantLang: LangPython,
			wantOK:   true,
		},
		{
			name:     "gunicorn binary",
			proc:     ProcessInfo{ExeName: "gunicorn", CmdLine: "gunicorn app:app"},
			wantLang: LangPython,
			wantOK:   true,
		},
		{
			name:     "uvicorn binary",
			proc:     ProcessInfo{ExeName: "uvicorn", CmdLine: "uvicorn main:app"},
			wantLang: LangPython,
			wantOK:   true,
		},
		{
			name:     "celery binary",
			proc:     ProcessInfo{ExeName: "celery", CmdLine: "celery -A tasks worker"},
			wantLang: LangPython,
			wantOK:   true,
		},
		{
			name:     "python3.10 versioned",
			proc:     ProcessInfo{ExeName: "python3.10", CmdLine: "python3.10 manage.py runserver"},
			wantLang: LangPython,
			wantOK:   true,
		},
		{
			name:     "pypy runtime",
			proc:     ProcessInfo{ExeName: "pypy3", CmdLine: "pypy3 server.py"},
			wantLang: LangPython,
			wantOK:   true,
		},
		{
			name:     ".py file in cmdline",
			proc:     ProcessInfo{ExeName: "some-wrapper", CmdLine: "/usr/bin/env manage.py runserver"},
			wantLang: LangPython,
			wantOK:   true,
		},
		{
			name:     "flask run pattern",
			proc:     ProcessInfo{ExeName: "flask", CmdLine: "flask run --host=0.0.0.0"},
			wantLang: LangPython,
			wantOK:   true,
		},
		{
			name:     "not python",
			proc:     ProcessInfo{ExeName: "node", CmdLine: "node server.js"},
			wantLang: "",
			wantOK:   false,
		},
	}

	inspector := &PythonInspector{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang, ok := inspector.Inspect(&tt.proc)
			if ok != tt.wantOK {
				t.Errorf("Inspect() ok = %v, want %v", ok, tt.wantOK)
			}
			if lang != tt.wantLang {
				t.Errorf("Inspect() lang = %q, want %q", lang, tt.wantLang)
			}
		})
	}
}

func TestLanguageRegistry_Detect(t *testing.T) {
	registry := NewLanguageRegistry()

	tests := []struct {
		name     string
		proc     ProcessInfo
		wantLang Language
		wantOK   bool
	}{
		{
			name:     "detects java",
			proc:     ProcessInfo{ExeName: "java", CmdLine: "java -jar app.jar"},
			wantLang: LangJava,
			wantOK:   true,
		},
		{
			name:     "detects node",
			proc:     ProcessInfo{ExeName: "node", CmdLine: "node server.js"},
			wantLang: LangNode,
			wantOK:   true,
		},
		{
			name:     "detects python",
			proc:     ProcessInfo{ExeName: "python3", CmdLine: "python3 app.py"},
			wantLang: LangPython,
			wantOK:   true,
		},
		{
			name:     "unknown process",
			proc:     ProcessInfo{ExeName: "nginx", CmdLine: "nginx -g daemon off"},
			wantLang: "",
			wantOK:   false,
		},
		{
			name:     "bash script (no match)",
			proc:     ProcessInfo{ExeName: "bash", CmdLine: "bash -c echo hello"},
			wantLang: "",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang, ok := registry.Detect(&tt.proc)
			if ok != tt.wantOK {
				t.Errorf("Detect() ok = %v, want %v", ok, tt.wantOK)
			}
			if lang != tt.wantLang {
				t.Errorf("Detect() lang = %q, want %q", lang, tt.wantLang)
			}
		})
	}
}

func TestLanguageRegistry_FirstMatchWins(t *testing.T) {
	// Java inspector is registered first, so "java" should match as Java
	// even though java could theoretically match something else
	registry := NewLanguageRegistry()

	proc := ProcessInfo{ExeName: "java", CmdLine: "java -jar app.jar"}
	lang, ok := registry.Detect(&proc)
	if !ok || lang != LangJava {
		t.Errorf("expected Java (first-match-wins), got lang=%q ok=%v", lang, ok)
	}
}

func TestIntegrationRegistry_Empty(t *testing.T) {
	registry := NewIntegrationRegistry()

	proc := ProcessInfo{ExeName: "redis-server", CmdLine: "redis-server /etc/redis.conf"}
	details, ok := registry.Detect(&proc)
	if ok {
		t.Errorf("empty registry should not match anything, got %+v", details)
	}
}
