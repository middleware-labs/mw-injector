package discovery

import "testing"

func TestJavaHandlerDetect(t *testing.T) {
	tests := []struct {
		name   string
		proc   ProcessInfo
		wantOK bool
	}{
		{
			name:   "standard java executable",
			proc:   ProcessInfo{ExeName: "java", CmdLine: "java -jar app.jar"},
			wantOK: true,
		},
		{
			name:   "java in path-like exe name",
			proc:   ProcessInfo{ExeName: "java", ExePath: "/usr/lib/jvm/java-17/bin/java", CmdLine: "/usr/lib/jvm/java-17/bin/java -jar app.jar"},
			wantOK: true,
		},
		{
			name:   "not java",
			proc:   ProcessInfo{ExeName: "python3", CmdLine: "python3 app.py"},
			wantOK: false,
		},
		{
			name:   "java in cmdline only",
			proc:   ProcessInfo{ExeName: "some-wrapper", CmdLine: "java -Xmx512m -jar service.jar"},
			wantOK: true,
		},
	}

	handler := &JavaHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok := handler.Detect(&tt.proc)
			if ok != tt.wantOK {
				t.Errorf("Detect() = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

func TestNodeHandlerDetect(t *testing.T) {
	tests := []struct {
		name   string
		proc   ProcessInfo
		wantOK bool
	}{
		{
			name:   "node executable",
			proc:   ProcessInfo{ExeName: "node", CmdLine: "node server.js"},
			wantOK: true,
		},
		{
			name:   "nodejs executable",
			proc:   ProcessInfo{ExeName: "nodejs", CmdLine: "nodejs app.js"},
			wantOK: true,
		},
		{
			name:   "npm resolves to node exe",
			proc:   ProcessInfo{ExeName: "node", CmdLine: "npm start"},
			wantOK: true,
		},
		{
			name:   "shell wrapper not detected",
			proc:   ProcessInfo{ExeName: "dash", CmdLine: "sh -c node index.js"},
			wantOK: false,
		},
		{
			name:   "not node",
			proc:   ProcessInfo{ExeName: "python3", CmdLine: "python3 app.py"},
			wantOK: false,
		},
	}

	handler := &NodeHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok := handler.Detect(&tt.proc)
			if ok != tt.wantOK {
				t.Errorf("Detect() = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

func TestPythonHandlerDetect(t *testing.T) {
	tests := []struct {
		name   string
		proc   ProcessInfo
		wantOK bool
	}{
		{
			name:   "python3 executable",
			proc:   ProcessInfo{ExeName: "python3", CmdLine: "python3 app.py"},
			wantOK: true,
		},
		{
			name:   "python executable",
			proc:   ProcessInfo{ExeName: "python", CmdLine: "python app.py"},
			wantOK: true,
		},
		{
			name:   "gunicorn binary",
			proc:   ProcessInfo{ExeName: "gunicorn", CmdLine: "gunicorn app:app"},
			wantOK: true,
		},
		{
			name:   "uvicorn binary",
			proc:   ProcessInfo{ExeName: "uvicorn", CmdLine: "uvicorn main:app"},
			wantOK: true,
		},
		{
			name:   "celery binary",
			proc:   ProcessInfo{ExeName: "celery", CmdLine: "celery -A tasks worker"},
			wantOK: true,
		},
		{
			name:   "python3.10 versioned",
			proc:   ProcessInfo{ExeName: "python3.10", CmdLine: "python3.10 manage.py runserver"},
			wantOK: true,
		},
		{
			name:   "pypy runtime",
			proc:   ProcessInfo{ExeName: "pypy3", CmdLine: "pypy3 server.py"},
			wantOK: true,
		},
		{
			name:   ".py file in cmdline",
			proc:   ProcessInfo{ExeName: "some-wrapper", CmdLine: "/usr/bin/env manage.py runserver"},
			wantOK: true,
		},
		{
			name:   "flask run pattern",
			proc:   ProcessInfo{ExeName: "flask", CmdLine: "flask run --host=0.0.0.0"},
			wantOK: true,
		},
		{
			name:   "not python",
			proc:   ProcessInfo{ExeName: "node", CmdLine: "node server.js"},
			wantOK: false,
		},
	}

	handler := &PythonHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok := handler.Detect(&tt.proc)
			if ok != tt.wantOK {
				t.Errorf("Detect() = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

func TestHandlerRegistry_Detect(t *testing.T) {
	registry := NewHandlerRegistry()

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

func TestHandlerRegistry_FirstMatchWins(t *testing.T) {
	registry := NewHandlerRegistry()

	proc := ProcessInfo{ExeName: "java", CmdLine: "java -jar app.jar"}
	lang, ok := registry.Detect(&proc)
	if !ok || lang != LangJava {
		t.Errorf("expected Java (first-match-wins), got lang=%q ok=%v", lang, ok)
	}
}

func TestHandlerRegistry_ForLanguage(t *testing.T) {
	registry := NewHandlerRegistry()

	if h := registry.ForLanguage(LangJava); h == nil {
		t.Error("expected Java handler, got nil")
	}
	if h := registry.ForLanguage(LangNode); h == nil {
		t.Error("expected Node handler, got nil")
	}
	if h := registry.ForLanguage(LangPython); h == nil {
		t.Error("expected Python handler, got nil")
	}
	if h := registry.ForLanguage(LangRust); h == nil {
		t.Error("expected Rust handler, got nil")
	}
	if h := registry.ForLanguage("golang"); h != nil {
		t.Error("expected nil for unregistered language, got non-nil")
	}
}

func TestIsNodeLauncher(t *testing.T) {
	tests := []struct {
		name    string
		cmdArgs []string
		want    bool
	}{
		{"null-separated npm start", []string{"npm", "start"}, true},
		{"null-separated npx serve", []string{"npx", "serve"}, true},
		{"null-separated yarn dev", []string{"yarn", "dev"}, true},
		{"space-joined npm start", []string{"npm start"}, true},
		{"space-joined npx create-app", []string{"npx create-react-app my-app"}, true},
		{"space-joined yarn run dev", []string{"yarn run dev"}, true},
		{"space-joined pnpm start", []string{"pnpm start"}, true},
		{"pm2 god daemon rewritten argv", []string{"PM2 v6.0.14: God Daemon (/home/user/.pm2)"}, true},
		{"pm2 null-separated", []string{"pm2", "start", "app.js"}, true},
		{"node running pm2-runtime script", []string{"node", "/usr/local/bin/pm2-runtime", "ecosystem.config.js"}, true},
		{"node running pm2-dev script", []string{"node", "/usr/local/lib/node_modules/pm2/bin/pm2-dev", "app.js"}, true},
		{"node running pm2 binary directly", []string{"node", "/usr/local/bin/pm2", "start", "app.js"}, true},
		{"node with flags before pm2-runtime", []string{"node", "--max-old-space-size=4096", "/usr/local/bin/pm2-runtime", "ecosystem.config.js"}, true},
		{"actual app process", []string{"node", "server.js"}, false},
		{"node with flags then app", []string{"node", "--inspect", "/app/server.js"}, false},
		{"node with only flags", []string{"node", "--inspect"}, false},
		{"entry point only", []string{"server.js"}, false},
		{"empty args", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNodeLauncher(tt.cmdArgs); got != tt.want {
				t.Errorf("isNodeLauncher(%v) = %v, want %v", tt.cmdArgs, got, tt.want)
			}
		})
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
