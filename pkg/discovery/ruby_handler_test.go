package discovery

import "testing"

func TestRubyHandler_Detect(t *testing.T) {
	h := &RubyHandler{}

	positives := []ProcessInfo{
		{PID: 1, ExeName: "ruby", ExePath: "/usr/bin/ruby", CmdLine: "ruby app.rb"},
		{PID: 2, ExeName: "jruby", ExePath: "/usr/bin/jruby", CmdLine: "jruby server.rb"},
		{PID: 3, ExeName: "rails", ExePath: "/usr/local/bin/rails", CmdLine: "rails server"},
		{PID: 4, ExeName: "puma", ExePath: "/usr/local/bin/puma", CmdLine: "puma -C config/puma.rb"},
		{PID: 5, ExeName: "sidekiq", ExePath: "/usr/local/bin/sidekiq", CmdLine: "sidekiq -C config/sidekiq.yml"},
		{PID: 6, ExeName: "unicorn", ExePath: "/usr/local/bin/unicorn", CmdLine: "unicorn -c config/unicorn.rb"},
		{PID: 7, ExeName: "bundler", ExePath: "/usr/local/bin/bundler", CmdLine: "bundler exec rails server"},
		{PID: 8, ExeName: "node", ExePath: "/usr/bin/node", CmdLine: "ruby worker.rb"},
		{PID: 9, ExeName: "bash", ExePath: "/bin/bash", CmdLine: "/opt/myapp/run.rb"},
	}

	for _, p := range positives {
		proc := p
		if !h.Detect(&proc) {
			t.Errorf("Detect should return true for %q (cmdline: %s)", proc.ExeName, proc.CmdLine)
		}
	}

	negatives := []ProcessInfo{
		{PID: 10, ExeName: "python3", ExePath: "/usr/bin/python3", CmdLine: "python3 app.py"},
		{PID: 11, ExeName: "node", ExePath: "/usr/bin/node", CmdLine: "node index.js"},
		{PID: 12, ExeName: "java", ExePath: "/usr/bin/java", CmdLine: "java -jar app.jar"},
		{PID: 13, ExeName: "nginx", ExePath: "/usr/sbin/nginx", CmdLine: "nginx -g daemon off"},
	}

	for _, p := range negatives {
		proc := p
		if h.Detect(&proc) {
			t.Errorf("Detect should return false for %q (cmdline: %s)", proc.ExeName, proc.CmdLine)
		}
	}
}

func TestRubyHandler_ExtractServiceName(t *testing.T) {
	h := &RubyHandler{}

	tests := []struct {
		name string
		proc *Process
		args []string
		want string
	}{
		{
			name: "exe name used as service name for puma",
			proc: &Process{
				PID:            1,
				ExecutableName: "puma",
				ExecutablePath: "/usr/local/bin/puma",
				Language:       LangRuby,
				Details:        map[string]any{},
			},
			args: []string{"puma"},
			want: "puma",
		},
		{
			name: "entry point .rb basename",
			proc: &Process{
				PID:            2,
				ExecutableName: "ruby",
				ExecutablePath: "/usr/bin/ruby",
				Language:       LangRuby,
				Details: map[string]any{
					DetailEntryPoint: "billing_worker.rb",
				},
			},
			args: []string{"ruby", "billing_worker.rb"},
			want: "billing-worker",
		},
		{
			name: "working directory fallback",
			proc: &Process{
				PID:            3,
				ExecutableName: "ruby",
				ExecutablePath: "/usr/bin/ruby",
				Language:       LangRuby,
				Details: map[string]any{
					DetailWorkingDirectory: "/opt/payment-api",
				},
			},
			args: []string{"ruby", "app.rb"},
			want: "payment-api",
		},
		{
			name: "fallback to ruby-service",
			proc: &Process{
				PID:            4,
				ExecutableName: "ruby",
				ExecutablePath: "/usr/bin/ruby",
				Language:       LangRuby,
				Details:        map[string]any{},
			},
			args: []string{"ruby"},
			want: "ruby-service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h.extractServiceName(tt.proc, tt.args)
			if tt.proc.ServiceName != tt.want {
				t.Errorf("extractServiceName → %q, want %q", tt.proc.ServiceName, tt.want)
			}
		})
	}
}

func TestRubyHandler_DetectProcessManager(t *testing.T) {
	h := &RubyHandler{}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"puma", []string{"puma", "-C", "config/puma.rb"}, "puma"},
		{"unicorn", []string{"unicorn", "-c", "config/unicorn.rb"}, "unicorn"},
		{"sidekiq", []string{"bundle", "exec", "sidekiq"}, "sidekiq"},
		{"passenger", []string{"passenger", "start"}, "passenger"},
		{"none", []string{"ruby", "script.rb"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &Process{Details: make(map[string]any)}
			h.detectProcessManager(proc, tt.args)
			got := proc.DetailString(DetailProcessManager)
			if got != tt.want {
				t.Errorf("detectProcessManager → %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRubyFingerprint(t *testing.T) {
	p1 := &Process{
		Language:       LangRuby,
		ExecutablePath: "/usr/bin/ruby",
		Details: map[string]any{
			DetailEntryPoint:       "config.ru",
			DetailWorkingDirectory: "/opt/myapp",
		},
	}

	p2 := &Process{
		Language:       LangRuby,
		ExecutablePath: "/usr/bin/ruby",
		Details: map[string]any{
			DetailEntryPoint:       "config.ru",
			DetailWorkingDirectory: "/opt/myapp",
		},
	}

	if p1.Fingerprint() != p2.Fingerprint() {
		t.Errorf("identical Ruby processes should produce same fingerprint")
	}

	p3 := &Process{
		Language:       LangRuby,
		ExecutablePath: "/usr/bin/ruby",
		Details: map[string]any{
			DetailEntryPoint:       "worker.rb",
			DetailWorkingDirectory: "/opt/other-app",
		},
	}

	if p1.Fingerprint() == p3.Fingerprint() {
		t.Errorf("different Ruby processes should produce different fingerprints")
	}
}
