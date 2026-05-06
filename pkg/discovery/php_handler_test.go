package discovery

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// --- Detect tests ---
// Based on real exe basenames from /proc/<pid>/exe across Debian, RHEL, cPanel,
// Plesk, DirectAdmin, CloudLinux, LiteSpeed, Docker official images, and FrankenPHP.

func TestPHPHandler_Detect(t *testing.T) {
	tests := []struct {
		name    string
		exeName string
		want    bool
	}{
		// PHP CLI — bare and versioned (Debian, RHEL, Remi, cPanel, Plesk, DirectAdmin)
		{"php bare", "php", true},
		{"php8.3", "php8.3", true},
		{"php8.2", "php8.2", true},
		{"php8.1", "php8.1", true},
		{"php8.0", "php8.0", true},
		{"php7.4", "php7.4", true},
		{"php5.6", "php5.6", true},
		{"php82 no dot", "php82", true},
		{"php83 no dot", "php83", true},

		// PHP-FPM — bare and versioned
		{"php-fpm bare", "php-fpm", true},
		{"php-fpm8.3", "php-fpm8.3", true},
		{"php-fpm8.2", "php-fpm8.2", true},
		{"php-fpm7.4", "php-fpm7.4", true},
		{"php-fpm82 no dot", "php-fpm82", true},

		// PHP-CGI — bare and versioned
		{"php-cgi bare", "php-cgi", true},
		{"php-cgi8.2", "php-cgi8.2", true},
		{"php-cgi7.4", "php-cgi7.4", true},

		// LiteSpeed LSAPI — LiteSpeed Enterprise, OpenLiteSpeed, CloudLinux, cPanel
		{"lsphp bare", "lsphp", true},
		{"lsphp82 no dot", "lsphp82", true},
		{"lsphp83 no dot", "lsphp83", true},
		{"lsphp8.2", "lsphp8.2", true},
		{"lsphp7.4", "lsphp7.4", true},

		// FrankenPHP — Go/Caddy + embedded PHP
		{"frankenphp", "frankenphp", true},

		// Negative cases — must NOT match
		{"phpstorm IDE", "phpstorm", false},
		{"php-cs-fixer linter", "php-cs-fixer", false},
		{"phpunit test runner", "phpunit", false},
		{"phpmyadmin", "phpmyadmin", false},
		{"python", "python", false},
		{"node", "node", false},
		{"java", "java", false},
		{"nginx (reverse proxy, not PHP)", "nginx", false},
		{"apache2 without mod_php (PID 0, no /proc)", "apache2", false},
		{"httpd without mod_php (PID 0, no /proc)", "httpd", false},
		{"caddy (not frankenphp)", "caddy", false},
		{"rr (RoadRunner Go binary)", "rr", false},
		{"dotnet (PeachPie)", "dotnet", false},
	}

	h := &PHPHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &ProcessInfo{ExeName: tt.exeName}
			got := h.Detect(proc)
			if got != tt.want {
				t.Errorf("Detect(%q) = %v, want %v", tt.exeName, got, tt.want)
			}
		})
	}
}

// --- Runtime classification tests ---
// Maps exe basenames to runtime types used in ServiceSetting and backend reporting.

func TestPHPHandler_ClassifyRuntime(t *testing.T) {
	tests := []struct {
		exeName string
		want    string
	}{
		// PHP-FPM
		{"php-fpm", "php-fpm"},
		{"php-fpm8.2", "php-fpm"},
		{"php-fpm8.3", "php-fpm"},
		{"php-fpm7.4", "php-fpm"},
		{"php-fpm82", "php-fpm"},

		// PHP-CGI
		{"php-cgi", "php-cgi"},
		{"php-cgi7.4", "php-cgi"},
		{"php-cgi8.2", "php-cgi"},

		// LiteSpeed
		{"lsphp", "litespeed"},
		{"lsphp82", "litespeed"},
		{"lsphp8.3", "litespeed"},

		// FrankenPHP
		{"frankenphp", "frankenphp"},

		// PHP CLI — default for bare php binaries
		{"php", "php-cli"},
		{"php8.2", "php-cli"},
		{"php8.3", "php-cli"},
		{"php7.4", "php-cli"},
	}

	for _, tt := range tests {
		t.Run(tt.exeName, func(t *testing.T) {
			got := classifyPHPRuntime(tt.exeName, 0)
			if got != tt.want {
				t.Errorf("classifyPHPRuntime(%q) = %q, want %q", tt.exeName, got, tt.want)
			}
		})
	}
}

// --- FPM master vs worker detection ---
// Real FPM master/worker process titles from ps aux on Debian, RHEL, Docker, cPanel, Plesk.

func TestPHPHandler_IsFPMMaster(t *testing.T) {
	tests := []struct {
		name    string
		cmdline string
		want    bool
	}{
		// Real FPM master process titles
		{
			"Debian FPM master",
			"php-fpm: master process (/etc/php/8.2/fpm/php-fpm.conf)",
			true,
		},
		{
			"RHEL FPM master",
			"php-fpm: master process (/etc/php-fpm.conf)",
			true,
		},
		{
			"Docker official php:fpm master",
			"php-fpm: master process (/usr/local/etc/php-fpm.conf)",
			true,
		},
		{
			"cPanel ea-php FPM master",
			"php-fpm: master process (/opt/cpanel/ea-php83/root/etc/php-fpm.conf)",
			true,
		},
		{
			"Plesk FPM master",
			"php-fpm: master process (/opt/plesk/php/8.3/etc/php-fpm.conf)",
			true,
		},
		{
			"Remi repo FPM master",
			"php-fpm: master process (/etc/opt/remi/php83/php-fpm.conf)",
			true,
		},
		// Real FPM worker process titles — must NOT be detected as master
		{
			"FPM worker default pool",
			"php-fpm: pool www",
			false,
		},
		{
			"FPM worker custom pool",
			"php-fpm: pool myapp",
			false,
		},
		{
			"cPanel per-domain pool worker",
			"php-fpm: pool example.com",
			false,
		},
		// Non-FPM processes
		{
			"Laravel artisan queue worker (supervisord)",
			"php /var/www/html/artisan queue:work --tries=3 --backoff=30 --timeout=120 --max-jobs=1000 --max-time=3600",
			false,
		},
		{
			"Symfony messenger worker",
			"php bin/console messenger:consume async --time-limit=3600",
			false,
		},
		{
			"FPM nodaemonize (not a master process title)",
			"php-fpm --nodaemonize",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isFPMMaster(tt.cmdline)
			if got != tt.want {
				t.Errorf("isFPMMaster(%q) = %v, want %v", tt.cmdline, got, tt.want)
			}
		})
	}
}

// --- FPM pool name extraction ---
// Real pool names from cPanel per-domain, Plesk, custom pool configs.

func TestPHPHandler_ExtractFPMPoolName(t *testing.T) {
	tests := []struct {
		name    string
		cmdline string
		want    string
	}{
		// Standard pool names
		{"default www pool", "php-fpm: pool www", "www"},
		{"custom pool", "php-fpm: pool myapp", "myapp"},
		{"pool with dashes", "php-fpm: pool my-web-app", "my-web-app"},

		// cPanel per-domain pool names
		{"cPanel domain pool", "php-fpm: pool example.com", "example.com"},
		{"cPanel subdomain pool", "php-fpm: pool api.example.com", "api.example.com"},

		// Hosting panel custom pools
		{"production pool", "php-fpm: pool production", "production"},
		{"staging pool", "php-fpm: pool staging", "staging"},
		{"api pool", "php-fpm: pool api", "api"},
		{"admin pool", "php-fpm: pool admin", "admin"},
		{"workers pool", "php-fpm: pool workers", "workers"},

		// Docker pool names
		{"Docker pool", "php-fpm: pool docker", "docker"},

		// No pool — master process or non-FPM
		{"FPM master", "php-fpm: master process (/etc/php-fpm.conf)", ""},
		{"PHP CLI", "php /var/www/index.php", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFPMPoolName(tt.cmdline)
			if got != tt.want {
				t.Errorf("extractFPMPoolName(%q) = %q, want %q", tt.cmdline, got, tt.want)
			}
		})
	}
}

// --- Script name → service name extraction ---
// Tests the generic name filter and subcommand support.

func TestPHPHandler_ExtractNameFromPHPScript(t *testing.T) {
	tests := []struct {
		name       string
		scriptName string
		want       string
	}{
		// Meaningful script names → become service names
		{"billing", "billing.php", "billing"},
		{"artisan (no subcommand)", "artisan", "artisan"},
		{"scheduler", "scheduler.php", "scheduler"},
		{"report phtml", "report.phtml", "report"},
		{"with path", "src/scheduler.php", "scheduler"},
		{"drush", "drush", "drush"},
		{"magento (bin/magento entry)", "magento", "magento"},
		{"wp (wp-cli)", "wp", "wp"},

		// Subcommand-enriched entry points → service names with dashes
		{"artisan:queue:work", "artisan:queue:work", "artisan-queue-work"},
		{"artisan:serve", "artisan:serve", "artisan-serve"},
		{"artisan:octane:start", "artisan:octane:start", "artisan-octane-start"},
		{"artisan:schedule:work", "artisan:schedule:work", "artisan-schedule-work"},
		{"artisan:horizon", "artisan:horizon", "artisan-horizon"},
		{"console:messenger:consume", "console:messenger:consume", "console-messenger-consume"},
		{"console:cache:clear", "console:cache:clear", "console-cache-clear"},

		// Generic scripts → filtered out (return empty)
		{"index.php", "index.php", ""},
		{"app.php", "app.php", ""},
		{"server.php (Laravel dev server script)", "server.php", ""},
		{"worker.php", "worker.php", ""},
		{"main.php", "main.php", ""},
		{"start.php (Workerman)", "start.php", ""},
		{"console (Symfony bare)", "console", ""},
		{"cron.php", "cron.php", ""},
		{"run.php", "run.php", ""},

		// Edge cases
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractNameFromPHPScript(tt.scriptName)
			if got != tt.want {
				t.Errorf("extractNameFromPHPScript(%q) = %q, want %q", tt.scriptName, got, tt.want)
			}
		})
	}
}

// --- PHP flag argument skipping ---
// The built-in dev server spawns: php -S 127.0.0.1:8000 router.php
// Without flag-arg skipping, "127.0.0.1:8000" becomes the entry point.

func TestPHPHandler_ExtractPHPInfoBuiltinServer(t *testing.T) {
	tests := []struct {
		name    string
		cmdArgs []string
		wantEP  string
	}{
		{
			"php -S with router script",
			[]string{"php", "-S", "127.0.0.1:8000", "public/index.php"},
			"index.php",
		},
		{
			"php -S with docroot -t flag",
			[]string{"php", "-S", "0.0.0.0:8000", "-t", "/var/www/public"},
			"",
		},
		{
			"php -S with -c config and -d flags",
			[]string{"php", "-c", "/etc/php.ini", "-d", "display_errors=0", "-S", "127.0.0.1:8000", "router.php"},
			"router.php",
		},
		{
			"php -d extension=opentelemetry then script",
			[]string{"php", "-d", "extension=opentelemetry", "/var/www/app/worker.php"},
			"worker.php",
		},
		{
			"php -d variables_order=EGPCS (Octane common pattern)",
			[]string{"/usr/bin/php", "-d", "variables_order=EGPCS", "/var/www/html/artisan", "octane:start", "--server=swoole"},
			"artisan:octane:start",
		},
	}

	h := &PHPHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &Process{
				PID:      99999,
				Language: LangPHP,
				Details:  make(map[string]any),
			}
			h.extractPHPInfo(proc, tt.cmdArgs)

			ep := proc.DetailString(DetailEntryPoint)
			if ep != tt.wantEP {
				t.Errorf("EntryPoint = %q, want %q", ep, tt.wantEP)
			}
		})
	}
}

// --- Subcommand extraction for artisan/console ---
// Real cmdlines from supervisord configs, Docker Compose, Kubernetes manifests.

func TestPHPHandler_ExtractPHPInfoSubcommand(t *testing.T) {
	tests := []struct {
		name    string
		cmdArgs []string
		wantEP  string
	}{
		// Laravel artisan — from real supervisord/Docker configs
		{
			"artisan serve",
			[]string{"php", "artisan", "serve"},
			"artisan:serve",
		},
		{
			"artisan queue:work with flags",
			[]string{"php", "/var/www/html/artisan", "queue:work", "--tries=3", "--backoff=30", "--timeout=120"},
			"artisan:queue:work",
		},
		{
			"artisan queue:work with queue name",
			[]string{"php", "artisan", "queue:work", "--queue=default,high", "--max-jobs=200"},
			"artisan:queue:work",
		},
		{
			"artisan schedule:run",
			[]string{"php", "artisan", "schedule:run", "--verbose", "--no-interaction"},
			"artisan:schedule:run",
		},
		{
			"artisan schedule:work",
			[]string{"php", "artisan", "schedule:work", "--env=production"},
			"artisan:schedule:work",
		},
		{
			"artisan horizon",
			[]string{"php", "artisan", "horizon"},
			"artisan:horizon",
		},
		{
			"artisan octane:start with swoole",
			[]string{"php", "artisan", "octane:start", "--server=swoole", "--workers=4", "--port=8000"},
			"artisan:octane:start",
		},
		{
			"artisan octane:start with roadrunner",
			[]string{"php", "artisan", "octane:start", "--server=roadrunner"},
			"artisan:octane:start",
		},
		{
			"artisan with -d flags before it",
			[]string{"php", "-d", "memory_limit=256M", "artisan", "queue:work"},
			"artisan:queue:work",
		},
		{
			"artisan bare (no subcommand — shows help)",
			[]string{"php", "artisan"},
			"artisan",
		},

		// Symfony bin/console — from real supervisord configs
		{
			"Symfony messenger:consume",
			[]string{"php", "bin/console", "messenger:consume", "async", "--time-limit=3600"},
			"console:messenger:consume",
		},
		{
			"Symfony cache:clear",
			[]string{"php", "bin/console", "cache:clear", "--env=prod"},
			"console:cache:clear",
		},

		// Magento bin/magento — subcommand NOT extracted (not in phpScriptsWithSubcommand)
		{
			"Magento cron:run",
			[]string{"php", "bin/magento", "cron:run"},
			"magento",
		},
		{
			"Magento indexer:reindex",
			[]string{"php", "/var/www/magento2/bin/magento", "indexer:reindex"},
			"magento",
		},

		// Regular scripts — no subcommand extraction
		{
			"regular php script",
			[]string{"php", "billing.php"},
			"billing.php",
		},
		{
			"RoadRunner worker script",
			[]string{"php", "psr-worker.php"},
			"psr-worker.php",
		},
		{
			"Swoole server script",
			[]string{"php", "/var/www/server.php"},
			"server.php",
		},
		{
			"Workerman start script",
			[]string{"php", "/var/www/start.php", "start", "-d"},
			"start.php",
		},
	}

	h := &PHPHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &Process{
				PID:      99999,
				Language: LangPHP,
				Details:  make(map[string]any),
			}
			h.extractPHPInfo(proc, tt.cmdArgs)

			ep := proc.DetailString(DetailEntryPoint)
			if ep != tt.wantEP {
				t.Errorf("EntryPoint = %q, want %q", ep, tt.wantEP)
			}
		})
	}
}

// --- Vendor path detection ---
// Laravel's built-in dev server spawns php with a vendor path script.

func TestPHPHandler_IsVendorPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/home/user/app/vendor/laravel/framework/src/Illuminate/Foundation/resources/server.php", true},
		{"/var/www/html/vendor/symfony/console/Application.php", true},
		{"/app/node_modules/.bin/something.php", true},
		{"/var/www/html/public/index.php", false},
		{"/var/www/html/artisan", false},
		{"artisan", false},
		{"/var/www/html/app/Console/Kernel.php", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isVendorPath(tt.path)
			if got != tt.want {
				t.Errorf("isVendorPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// --- Vendor path should not override working directory ---
// Catches the "illuminate-foundation" bug from Laravel's built-in dev server.

func TestPHPHandler_ExtractPHPInfoVendorPath(t *testing.T) {
	h := &PHPHandler{}
	proc := &Process{
		PID:      99999,
		Language: LangPHP,
		Details:  make(map[string]any),
	}

	cmdArgs := []string{
		"/usr/bin/php8.2",
		"-S", "127.0.0.1:8000",
		"/home/user/app/vendor/laravel/framework/src/Illuminate/Foundation/resources/server.php",
	}
	h.extractPHPInfo(proc, cmdArgs)

	ep := proc.DetailString(DetailEntryPoint)
	if ep != "server.php" {
		t.Errorf("EntryPoint = %q, want %q", ep, "server.php")
	}

	// Working directory should NOT be overridden to the vendor path
	wd := proc.DetailString(DetailWorkingDirectory)
	if wd != "" {
		t.Errorf("WorkingDirectory = %q, want empty (vendor path should not override CWD)", wd)
	}
}

// --- Composer.json reading ---
// Real composer.json "name" fields from popular PHP projects.

func TestPHPHandler_ExtractPHPInfoComposerJson(t *testing.T) {
	tests := []struct {
		name        string
		composer    string
		wantPkgName string
	}{
		// Real project composer.json
		{
			"Laravel project",
			`{"name": "laravel/laravel", "type": "project"}`,
			"laravel/laravel",
		},
		{
			"Custom Laravel project",
			`{"name": "acme/billing-api", "type": "project"}`,
			"acme/billing-api",
		},
		{
			"Drupal project",
			`{"name": "drupal/recommended-project", "type": "project"}`,
			"drupal/recommended-project",
		},
		{
			"Symfony project",
			`{"name": "symfony/skeleton", "type": "project"}`,
			"symfony/skeleton",
		},
		{
			"WordPress Composer-managed (Bedrock)",
			`{"name": "roots/bedrock", "type": "project"}`,
			"roots/bedrock",
		},
		{
			"Magento project",
			`{"name": "magento/magento2ce", "type": "project"}`,
			"magento/magento2ce",
		},
		{
			"Custom API project",
			`{"name": "middleware/payment-gateway", "type": "project"}`,
			"middleware/payment-gateway",
		},
		// Edge cases
		{
			"no name field",
			`{"type": "project", "require": {"php": "^8.2"}}`,
			"",
		},
		{
			"empty name",
			`{"name": ""}`,
			"",
		},
		{
			"invalid json",
			`{not valid}`,
			"",
		},
	}

	h := &PHPHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(tt.composer), 0644); err != nil {
				t.Fatal(err)
			}

			proc := &Process{
				PID:      99999,
				Language: LangPHP,
				Details:  make(map[string]any),
			}
			h.readComposerJson(proc, dir)

			got := proc.DetailString(DetailPackageName)
			if got != tt.wantPkgName {
				t.Errorf("DetailPackageName = %q, want %q", got, tt.wantPkgName)
			}
		})
	}
}

// --- Service name extraction (full priority chain) ---
// Tests the 7-level priority chain with realistic data combinations.

func TestPHPHandler_ExtractServiceName(t *testing.T) {
	tests := []struct {
		name        string
		details     map[string]any
		wantService string
	}{
		// Level 4: FPM pool name (skip "www", use meaningful names)
		{
			"FPM custom pool becomes service name",
			map[string]any{DetailPHPPoolName: "billing-workers"},
			"billing-workers",
		},
		{
			"FPM domain pool from cPanel (dots stripped by cleanName)",
			map[string]any{DetailPHPPoolName: "example.com"},
			"examplecom",
		},
		{
			"FPM api pool",
			map[string]any{DetailPHPPoolName: "api"},
			"api",
		},
		{
			"FPM www pool skipped → falls to entry point",
			map[string]any{
				DetailPHPPoolName: "www",
				DetailEntryPoint:  "scheduler.php",
			},
			"scheduler",
		},
		{
			"FPM www pool skipped → falls to composer package",
			map[string]any{
				DetailPHPPoolName: "www",
				DetailPackageName: "acme/order-service",
			},
			"order-service",
		},

		// Level 5: Script filename
		{
			"artisan:queue:work entry point → service name",
			map[string]any{DetailEntryPoint: "artisan:queue:work"},
			"artisan-queue-work",
		},
		{
			"artisan:horizon entry point",
			map[string]any{DetailEntryPoint: "artisan:horizon"},
			"artisan-horizon",
		},
		{
			"console:messenger:consume",
			map[string]any{DetailEntryPoint: "console:messenger:consume"},
			"console-messenger-consume",
		},
		{
			"drush entry point",
			map[string]any{DetailEntryPoint: "drush"},
			"drush",
		},
		{
			"magento entry point",
			map[string]any{DetailEntryPoint: "magento"},
			"magento",
		},
		{
			"generic index.php skipped → falls to composer",
			map[string]any{
				DetailEntryPoint:  "index.php",
				DetailPackageName: "acme/storefront",
			},
			"storefront",
		},
		{
			"generic server.php (Laravel dev server) skipped → workdir fallback",
			map[string]any{
				DetailEntryPoint:       "server.php",
				DetailWorkingDirectory: "/var/www/html/laragigs",
			},
			"html-laragigs",
		},

		// Level 6: Composer package name (use last segment of vendor/package)
		{
			"Laravel project composer name",
			map[string]any{DetailPackageName: "laravel/laravel"},
			"laravel",
		},
		{
			"Custom package",
			map[string]any{DetailPackageName: "middleware/payment-gateway"},
			"payment-gateway",
		},
		{
			"Drupal project",
			map[string]any{DetailPackageName: "drupal/recommended-project"},
			"recommended-project",
		},

		// Level 7: Working directory (last 2 meaningful segments after filtering generics)
		{
			"working directory fallback",
			map[string]any{DetailWorkingDirectory: "/var/www/html/my-laravel-app"},
			"html-my-laravel-app",
		},
		{
			"working directory with generic parent",
			map[string]any{DetailWorkingDirectory: "/home/deploy/apps/ecommerce-api"},
			"deploy-ecommerce-api",
		},

		// Level 8: Ultimate fallback
		{
			"no signals → php-service",
			map[string]any{},
			"php-service",
		},
	}

	h := &PHPHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &Process{
				PID:      99999,
				Language: LangPHP,
				Details:  tt.details,
			}
			h.extractServiceName(proc)

			if proc.ServiceName != tt.wantService {
				t.Errorf("ServiceName = %q, want %q", proc.ServiceName, tt.wantService)
			}
		})
	}
}

// --- Full integration: composer.json → service name ---
// Simulates the real enrichment path where readComposerJson populates
// DetailPackageName, then extractServiceName uses it.

func TestPHPHandler_ServiceNameFromComposer(t *testing.T) {
	tests := []struct {
		name        string
		composer    string
		wantService string
	}{
		{
			"laravel/laravel → laravel",
			`{"name": "laravel/laravel"}`,
			"laravel",
		},
		{
			"acme/billing-api → billing-api",
			`{"name": "acme/billing-api"}`,
			"billing-api",
		},
		{
			"roots/bedrock → bedrock",
			`{"name": "roots/bedrock"}`,
			"bedrock",
		},
	}

	h := &PHPHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(tt.composer), 0644); err != nil {
				t.Fatal(err)
			}

			proc := &Process{
				PID:      99999,
				Language: LangPHP,
				Details: map[string]any{
					DetailWorkingDirectory: dir,
				},
			}

			h.readComposerJson(proc, dir)
			h.extractServiceName(proc)

			if proc.ServiceName != tt.wantService {
				t.Errorf("ServiceName = %q, want %q", proc.ServiceName, tt.wantService)
			}
		})
	}
}

// --- Instrumentation detection ---
// Tests detection of OTel PHP extension via cmdline flags and env vars.

func TestPHPHandler_DetectInstrumentation(t *testing.T) {
	tests := []struct {
		name         string
		cmdline      string
		wantHasAgent bool
	}{
		{
			"OTel extension via -d flag",
			"php -d extension=opentelemetry /var/www/artisan queue:work",
			true,
		},
		{
			"OTel zend_extension via -d flag",
			"php -d zend_extension=opentelemetry /var/www/server.php",
			true,
		},
		{
			"no instrumentation",
			"php /var/www/html/artisan queue:work --tries=3",
			false,
		},
		{
			"unrelated extension",
			"php -d extension=xdebug /var/www/server.php",
			false,
		},
	}

	h := &PHPHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &Process{
				PID:         99999,
				Language:    LangPHP,
				CommandLine: tt.cmdline,
			}
			h.detectInstrumentation(proc)

			if proc.HasAgent != tt.wantHasAgent {
				t.Errorf("HasAgent = %v, want %v", proc.HasAgent, tt.wantHasAgent)
			}
		})
	}
}

// --- libphp regex for mod_php detection ---
// Real /proc/<pid>/maps lines from Debian, RHEL, cPanel hosting.

func TestPHPHandler_LibPHPRegex(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		match bool
	}{
		// Real maps entries from various distros
		{
			"Debian libphp8.2",
			"7fa1234567890000-7fa1298765432000 r-xp 00000000 ca:01 12345 /usr/lib/apache2/modules/libphp8.2.so",
			true,
		},
		{
			"RHEL libphp.so (unversioned)",
			"7f1234000000-7f1234500000 r-xp 00000000 fd:00 67890 /usr/lib64/httpd/modules/libphp.so",
			true,
		},
		{
			"libphp7.4 (older version)",
			"7f0000000000-7f0000400000 r-xp 00000000 ca:01 11111 /usr/lib/apache2/modules/libphp7.4.so",
			true,
		},
		{
			"cPanel ea-php82 libphp",
			"7fa000000000-7fa000500000 r-xp 00000000 08:01 22222 /opt/cpanel/ea-php82/root/usr/lib64/libphp.so",
			true,
		},
		{
			"cPanel ea-php83 versioned",
			"7fa000000000-7fa000500000 r-xp 00000000 08:01 33333 /opt/cpanel/ea-php83/root/usr/lib64/libphp8.3.so",
			true,
		},
		{
			"libphp82 (no dot version)",
			"7f0000000000-7f0000100000 r-xp 00000000 ca:01 44444 /usr/lib/libphp82.so",
			true,
		},
		{
			"Debian embed variant",
			"7f0000000000-7f0000100000 r-xp 00000000 ca:01 55555 /usr/lib/libphp8.2-embed.so",
			false,
		},

		// Non-PHP shared objects — must NOT match
		{
			"mod_rewrite (not PHP)",
			"7f1234000000-7f1234100000 r-xp 00000000 fd:00 99999 /usr/lib/apache2/modules/mod_rewrite.so",
			false,
		},
		{
			"libc.so (system library)",
			"7f0000000000-7f0000200000 r-xp 00000000 ca:01 11111 /usr/lib/x86_64-linux-gnu/libc.so.6",
			false,
		},
		{
			"libssl (not PHP)",
			"7f0000000000-7f0000100000 r-xp 00000000 ca:01 22222 /usr/lib/x86_64-linux-gnu/libssl.so.3",
			false,
		},
		{
			"mod_php_custom (hypothetical, should not match without libphp)",
			"7f1234000000-7f1234100000 r-xp 00000000 fd:00 99999 /usr/lib/apache2/modules/mod_php.so",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := libphpRegex.MatchString(tt.line)
			if got != tt.match {
				t.Errorf("libphpRegex.MatchString(%q) = %v, want %v", tt.line, got, tt.match)
			}
		})
	}
}

// --- nextPositionalArg helper ---

func TestPHPHandler_NextPositionalArg(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		start int
		want  string
	}{
		{
			"subcommand after artisan",
			[]string{"php", "artisan", "queue:work", "--tries=3"},
			2, "queue:work",
		},
		{
			"skip flags to find subcommand",
			[]string{"php", "artisan", "--verbose", "queue:work"},
			2, "queue:work",
		},
		{
			"no more positional args",
			[]string{"php", "artisan", "--tries=3", "--timeout=90"},
			2, "",
		},
		{
			"start beyond array",
			[]string{"php", "artisan"},
			5, "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextPositionalArg(tt.args, tt.start)
			if got != tt.want {
				t.Errorf("nextPositionalArg(%v, %d) = %q, want %q", tt.args, tt.start, got, tt.want)
			}
		})
	}
}

// --- Handler registry integration ---
// Verifies PHPHandler is registered and Detect works through the registry.

func TestPHPHandler_DetectInRegistry(t *testing.T) {
	registry := NewHandlerRegistry()

	tests := []struct {
		name     string
		proc     ProcessInfo
		wantLang Language
		wantOK   bool
	}{
		{
			"detects php-fpm worker",
			ProcessInfo{ExeName: "php-fpm8.2", CmdLine: "php-fpm: pool www"},
			LangPHP,
			true,
		},
		{
			"detects php cli",
			ProcessInfo{ExeName: "php", CmdLine: "php /var/www/app.php"},
			LangPHP,
			true,
		},
		{
			"detects frankenphp",
			ProcessInfo{ExeName: "frankenphp", CmdLine: "frankenphp run --config /etc/caddy/Caddyfile"},
			LangPHP,
			true,
		},
		{
			"detects lsphp (LiteSpeed)",
			ProcessInfo{ExeName: "lsphp82", CmdLine: "lsphp82"},
			LangPHP,
			true,
		},
		{
			"does not detect phpstorm",
			ProcessInfo{ExeName: "phpstorm", CmdLine: "phpstorm"},
			"",
			false,
		},
		{
			"does not detect nginx",
			ProcessInfo{ExeName: "nginx", CmdLine: "nginx: worker process"},
			"",
			false,
		},
		{
			"does not detect RoadRunner Go binary",
			ProcessInfo{ExeName: "rr", CmdLine: "rr serve -c .rr.yaml"},
			"",
			false,
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

// --- Real-world end-to-end scenarios ---
// Combines extractPHPInfo + extractServiceName to simulate the full discovery
// path for real production PHP deployment patterns.

func TestPHPHandler_RealWorldScenarios(t *testing.T) {
	h := &PHPHandler{}

	tests := []struct {
		name        string
		cmdArgs     []string
		cmdLine     string
		details     map[string]any
		composer    string
		wantEP      string
		wantService string
	}{
		{
			name:    "Laravel queue worker via supervisord",
			cmdArgs: []string{"php", "/var/www/html/artisan", "queue:work", "--tries=3", "--backoff=30", "--timeout=120", "--max-jobs=1000"},
			cmdLine: "php /var/www/html/artisan queue:work --tries=3 --backoff=30 --timeout=120 --max-jobs=1000",
			wantEP:  "artisan:queue:work",
		},
		{
			name:    "Laravel Horizon",
			cmdArgs: []string{"php", "artisan", "horizon"},
			cmdLine: "php artisan horizon",
			wantEP:  "artisan:horizon",
		},
		{
			name:    "Laravel Octane with Swoole",
			cmdArgs: []string{"php", "artisan", "octane:start", "--server=swoole", "--workers=4", "--port=8000"},
			cmdLine: "php artisan octane:start --server=swoole --workers=4 --port=8000",
			wantEP:  "artisan:octane:start",
		},
		{
			name:    "Laravel Octane with RoadRunner (PHP worker side)",
			cmdArgs: []string{"php", "/var/www/app/artisan", "octane:start", "--server=roadrunner"},
			cmdLine: "php /var/www/app/artisan octane:start --server=roadrunner",
			wantEP:  "artisan:octane:start",
		},
		{
			name:    "Symfony messenger consumer",
			cmdArgs: []string{"php", "bin/console", "messenger:consume", "async", "--time-limit=3600"},
			cmdLine: "php bin/console messenger:consume async --time-limit=3600",
			wantEP:  "console:messenger:consume",
		},
		{
			name:    "Magento cron:run",
			cmdArgs: []string{"php", "/var/www/magento2/bin/magento", "cron:run", "--group=default"},
			cmdLine: "php /var/www/magento2/bin/magento cron:run --group=default",
			wantEP:  "magento",
		},
		{
			name:    "Magento indexer:reindex with threads",
			cmdArgs: []string{"php", "-f", "bin/magento", "indexer:reindex", "catalogsearch_fulltext"},
			cmdLine: "php -f bin/magento indexer:reindex catalogsearch_fulltext",
			wantEP:  "magento",
		},
		{
			name:    "Drupal drush cron",
			cmdArgs: []string{"php", "/usr/local/bin/drush", "--root=/var/www/drupal", "cron"},
			cmdLine: "php /usr/local/bin/drush --root=/var/www/drupal cron",
			wantEP:  "drush",
		},
		{
			name:    "WordPress WP-CLI cron",
			cmdArgs: []string{"php", "/usr/local/bin/wp", "--path=/var/www/html", "cron", "event", "run", "--due-now"},
			cmdLine: "php /usr/local/bin/wp --path=/var/www/html cron event run --due-now",
			wantEP:  "wp",
		},
		{
			name:    "FrankenPHP run with Caddyfile",
			cmdArgs: []string{"frankenphp", "run", "--config", "/etc/caddy/Caddyfile"},
			cmdLine: "frankenphp run --config /etc/caddy/Caddyfile",
			wantEP:  "run",
		},
		{
			name:    "FrankenPHP php-server mode",
			cmdArgs: []string{"frankenphp", "php-server"},
			cmdLine: "frankenphp php-server",
			wantEP:  "php-server",
		},
		{
			name:    "RoadRunner PHP worker (psr-worker.php)",
			cmdArgs: []string{"php", "psr-worker.php"},
			cmdLine: "php psr-worker.php",
			wantEP:  "psr-worker.php",
		},
		{
			name:    "Workerman start script",
			cmdArgs: []string{"php", "/var/www/start.php", "start", "-d"},
			cmdLine: "php /var/www/start.php start -d",
			wantEP:  "start.php",
		},
		{
			name:    "php-fpm worker with pool name",
			cmdArgs: []string{"php-fpm8.2"},
			cmdLine: "php-fpm: pool billing-workers",
			wantEP:  "",
		},
		{
			// artisan:serve is skipped in Enrich() — it's Laravel's dev server
			// wrapper that spawns a child php -S process. We verify the entry
			// point is correctly identified so the skip triggers.
			name:    "Laravel artisan serve (skipped in Enrich)",
			cmdArgs: []string{"php", "artisan", "serve"},
			cmdLine: "php artisan serve",
			wantEP:  "artisan:serve",
		},
		{
			name:    "Laravel artisan serve with host/port",
			cmdArgs: []string{"php", "artisan", "serve", "--host=0.0.0.0", "--port=8080"},
			cmdLine: "php artisan serve --host=0.0.0.0 --port=8080",
			wantEP:  "artisan:serve",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &Process{
				PID:         99999,
				Language:    LangPHP,
				CommandLine: tt.cmdLine,
				Details:     make(map[string]any),
			}
			maps.Copy(proc.Details, tt.details)

			h.extractPHPInfo(proc, tt.cmdArgs)

			ep := proc.DetailString(DetailEntryPoint)
			if ep != tt.wantEP {
				t.Errorf("EntryPoint = %q, want %q", ep, tt.wantEP)
			}

			// If we have a temp dir with composer.json, read it
			if tt.composer != "" {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(tt.composer), 0644); err != nil {
					t.Fatal(err)
				}
				proc.Details[DetailWorkingDirectory] = dir
				h.readComposerJson(proc, dir)
			}

			if tt.wantService != "" {
				h.extractServiceName(proc)
				if proc.ServiceName != tt.wantService {
					t.Errorf("ServiceName = %q, want %q", proc.ServiceName, tt.wantService)
				}
			}
		})
	}
}

// --- FPM pool name as service name (production scenarios) ---
// Verifies that production pool names flow correctly through the service name chain.

func TestPHPHandler_FPMPoolServiceName(t *testing.T) {
	tests := []struct {
		name        string
		poolName    string
		wantService string
	}{
		{"custom pool", "billing-workers", "billing-workers"},
		{"domain pool (cPanel, dots stripped by cleanName)", "example.com", "examplecom"},
		{"api pool", "api", "api"},
		{"staging pool", "staging", "staging"},
	}

	h := &PHPHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := &Process{
				PID:      99999,
				Language: LangPHP,
				Details: map[string]any{
					DetailPHPPoolName: tt.poolName,
				},
			}
			h.extractServiceName(proc)

			if proc.ServiceName != tt.wantService {
				t.Errorf("ServiceName = %q, want %q", proc.ServiceName, tt.wantService)
			}
		})
	}
}
