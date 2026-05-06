package discovery

import (
	"bytes"
	"debug/elf"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	// Caps prevent excessive I/O and memory use during ELF inspection.
	// 200MB covers every realistic Rust binary; anything larger is almost
	// certainly a C++ mega-binary (browser, game engine) that happens to
	// link some Rust code.
	rustMaxBinarySize  = 200 * 1024 * 1024
	rustMaxSectionSize = 50 * 1024 * 1024
)

// RustHandler implements LanguageHandler for Rust processes.
//
// Unlike interpreted languages (Java, Python, Node) where the runtime binary
// name is the giveaway, Rust compiles to native ELF binaries with no obvious
// filename convention. Detection therefore relies on ELF metadata — compiler
// stamps and mangled symbol names — applied in cheapest-first order with
// explicit guards against C++/Rust hybrid binaries (browsers, Electron apps)
// that ship Rust components but aren't Rust applications.
type RustHandler struct{}

func (h *RustHandler) Lang() Language { return LangRust }

func (h *RustHandler) Detect(proc *ProcessInfo) bool {
	return isRustBinary(proc.PID, proc.ExePath)
}

func (h *RustHandler) Enrich(info *ProcessInfo, opts DiscoveryOptions, detector *ContainerDetector) *Process {
	pid := info.PID
	cmdArgs := info.CmdArgs

	owner := readProcessOwner(pid)
	createTime := readProcessCreateTime(pid)
	alignedTime := (createTime / 1000) * 1000

	if cached, hit := GetCachedProcessMetadata(pid, alignedTime); hit {
		if cached.Ignore {
			return nil
		}

		status := readProcessStatus(pid)

		return &Process{
			PID:            pid,
			ParentPID:      readProcessPPID(pid),
			ExecutableName: info.ExeName,
			ExecutablePath: info.ExePath,
			CommandLine:    info.CmdLine,
			Owner:          cached.Owner,
			CreateTime:     timeFromMillis(createTime),
			Status:         status,
			Language:       LangRust,

			ServiceName:        cached.ServiceName,
			RuntimeName:        "rust",
			RuntimeDescription: "Rust Native Binary",

			HasAgent:          cached.HasAgent,
			IsMiddlewareAgent: cached.IsMiddlewareAgent,
			AgentPath:         cached.AgentPath,
			AgentType:         cached.AgentType,
			ContainerInfo:     cached.ContainerInfo,

			Details: map[string]any{
				DetailEntryPoint:          cached.EntryPoint,
				DetailSystemdUnit:         cached.SystemdUnit,
				DetailExplicitServiceName: cached.ExplicitServiceName,
				DetailWorkingDirectory:    cached.WorkingDirectory,
			},
		}
	}

	if isIgnoredSystemdUnit(pid) {
		CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{Ignore: true})
		return nil
	}

	status := readProcessStatus(pid)

	proc := &Process{
		PID:            pid,
		ParentPID:      readProcessPPID(pid),
		ExecutableName: info.ExeName,
		ExecutablePath: info.ExePath,
		CommandLine:    info.CmdLine,
		CommandArgs:    cmdArgs,
		Owner:          owner,
		CreateTime:     timeFromMillis(createTime),
		Status:         status,
		Language:       LangRust,

		RuntimeName:        "rust",
		RuntimeDescription: "Rust Native Binary",

		Details: make(map[string]any),
	}

	if opts.IncludeContainerInfo {
		containerInfo, err := detector.IsProcessInContainer(pid)
		if err == nil && containerInfo.IsContainer {
			proc.ContainerInfo = containerInfo
		}
	}

	proc.Details[DetailEntryPoint] = info.ExeName
	enrichCommonDetails(proc)
	h.extractServiceName(proc, info)
	h.detectInstrumentation(proc)

	CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{
		ServiceName:         proc.ServiceName,
		EntryPoint:          proc.DetailString(DetailEntryPoint),
		HasAgent:            proc.HasAgent,
		IsMiddlewareAgent:   proc.IsMiddlewareAgent,
		AgentPath:           proc.AgentPath,
		AgentType:           proc.AgentType,
		ContainerInfo:       proc.ContainerInfo,
		Owner:               proc.Owner,
		SystemdUnit:         proc.DetailString(DetailSystemdUnit),
		ExplicitServiceName: proc.DetailString(DetailExplicitServiceName),
		WorkingDirectory:    proc.DetailString(DetailWorkingDirectory),
	})

	return proc
}

func (h *RustHandler) PassesFilter(proc *Process, filter ProcessFilter) bool {
	if filter.CurrentUserOnly {
		return proc.Owner == currentUser()
	}
	return true
}

func (h *RustHandler) ToServiceSetting(proc *Process) *ServiceSetting {
	key := fmt.Sprintf("host-rust-%s", sanitize(proc.ServiceName))
	isSystemd, unitname := CheckSystemdStatus(proc.PID)
	serviceType := "system"
	if isSystemd {
		serviceType = "systemd"
	}

	if proc.IsInContainer() {
		serviceType = "docker"
		if proc.ContainerInfo.ContainerID != "" && len(proc.ContainerInfo.ContainerID) >= 12 {
			key = fmt.Sprintf("docker-rust-%s", proc.ContainerInfo.ContainerID[:12])
		}
	}

	return &ServiceSetting{
		PID:         proc.PID,
		ServiceName: proc.ServiceName,
		Owner:       proc.Owner,
		Status:      proc.Status,
		Enabled:     true,
		ServiceType: serviceType,
		Language:    "rust",

		HasAgent:          proc.HasAgent,
		IsMiddlewareAgent: proc.IsMiddlewareAgent,
		AgentType:         proc.AgentType,
		AgentPath:         proc.AgentPath,
		Instrumented:      proc.HasAgent,

		Key:         key,
		SystemdUnit: unitname,
		Listeners:   proc.Listeners(),
		Fingerprint: proc.Fingerprint(),
	}
}

// --- Service name extraction ---

// extractServiceName determines a human-readable service identity for a Rust
// process. The priority order reflects signal reliability: explicit user config
// beats runtime inference, and runtime-provided names beat filesystem heuristics.
func (h *RustHandler) extractServiceName(proc *Process, info *ProcessInfo) {
	// Level 1: User-defined name via OTEL_SERVICE_NAME or MW_SERVICE_NAME env vars.
	if name := extractServiceNameFromEnviron(proc.PID); name != "" {
		proc.ServiceName = cleanName(name)
		return
	}

	// Level 2: Container name (docker/podman assign meaningful names).
	if proc.ContainerInfo != nil && proc.ContainerInfo.IsContainer {
		if proc.ContainerInfo.ContainerName != "" {
			proc.ServiceName = proc.ContainerInfo.ContainerName
			return
		}
	}

	// Level 3: Systemd unit name (e.g. "my-api.service" → "my-api").
	if unit := proc.DetailString(DetailSystemdUnit); unit != "" {
		if name := cleanName(unit); name != "" {
			proc.ServiceName = name
			return
		}
	}

	// Level 4: Executable basename. Rust's cargo names binaries after the crate
	// by default, so the exe name is usually a good service identity.
	if name := cleanName(info.ExeName); name != "" {
		proc.ServiceName = name
		return
	}

	// Level 5: Working directory as last resort before generic fallback.
	workDir := proc.DetailString(DetailWorkingDirectory)
	if workDir != "" {
		if name := serviceNameFromWorkDir(workDir); name != "" {
			proc.ServiceName = name
			return
		}
	}

	proc.ServiceName = "rust-service"
}

// detectInstrumentation checks whether this Rust process already has
// observability instrumentation active. Rust doesn't have a standard
// auto-instrumentation agent like Java (javaagent) or Python (sitecustomize),
// so detection is limited to two mechanisms:
//   - OTel SDK env vars: the app was compiled with an OTel tracing crate and
//     configured via standard OTEL_* environment variables.
//   - LD_PRELOAD injection: OBI (eBPF-based instrumentation) injects a shared
//     library via LD_PRELOAD without modifying the binary.
func (h *RustHandler) detectInstrumentation(proc *Process) {
	environPath := fmt.Sprintf("/proc/%d/environ", proc.PID)
	data, err := os.ReadFile(environPath)
	if err != nil {
		return
	}
	env := string(data)

	if strings.Contains(env, "OTEL_TRACES_EXPORTER=") || strings.Contains(env, "OTEL_EXPORTER_OTLP_ENDPOINT=") {
		proc.HasAgent = true
		proc.AgentType = "opentelemetry"
	}

	if strings.Contains(env, "LD_PRELOAD=") && strings.Contains(env, "libotelinject.so") {
		proc.HasAgent = true
		proc.AgentType = "otel-injector"
		proc.AgentPath = extractLibOtelInjectPath(extractEnvValue(env, "LD_PRELOAD"))
	}
}

// --- ELF-based Rust detection ---

// isRustBinary determines whether an ELF binary is a Rust application by
// inspecting compiler metadata and symbols. The challenge is distinguishing
// "apps written in Rust" from "apps that link Rust code as a component" —
// browsers like Chrome and Firefox ship Rust crates internally but are C++
// programs. Instrumenting those as Rust would be wrong.
//
// The pipeline is ordered cheapest-to-most-expensive:
//
//  1. File size gate — skip anything over 200MB. Realistic Rust binaries are
//     well under this; mega-binaries are almost always C++ (browsers, game
//     engines) that happen to link Rust staticlibs.
//
//  2. .comment section — the ELF .comment section contains compiler version
//     strings. Since rustc 1.73 (Oct 2023), rustc writes "rustc version X.Y.Z
//     (...)" here via LLVM's llvm.ident metadata. If "rustc" is absent, this
//     is not a Rust binary. The .comment section survives GNU strip but is
//     removed by llvm-strip (used by Cargo's strip = "symbols").
//
//  3. Clang co-compiler rejection — if .comment also contains "clang version",
//     the binary has C/C++ compilation units built with clang alongside Rust.
//     This pattern identifies hybrid binaries (browsers, Electron). Pure Rust
//     apps only have rustc + GCC (from libc) in .comment, never clang.
//
//  4. lang_start symbol — std::rt::lang_start is the Rust runtime entry point
//     that wraps fn main(). It only exists when Rust owns the program's main().
//     Hybrid binaries that call Rust from a C/C++ main have lang_start_internal
//     but not lang_start itself. This is the definitive test when .symtab is
//     available.
//
//  5. Stripped fallback — if .symtab was removed (GNU strip), we still have
//     .comment. We accept only if the rustc version string matches an official
//     release format (stable/beta/nightly). Vendor forks used by browsers emit
//     non-standard version strings like "rustc X.Y.Z-dev (...chromium)" that
//     won't match, filtering out stripped hybrid binaries.
func isRustBinary(pid int32, exePath string) bool {
	if exePath == "" {
		return false
	}

	// Resolve the path to open. ExePath comes from readlink(/proc/<pid>/exe)
	// which returns the container-internal path (e.g. /usr/local/bin/app).
	// That path doesn't exist on the host for containerized processes. In that
	// case, open /proc/<pid>/exe directly — the kernel's magic symlink lets us
	// read the binary across mount namespaces.
	openPath := exePath
	stat, err := os.Stat(openPath)
	if err != nil {
		openPath = fmt.Sprintf("/proc/%d/exe", pid)
		stat, err = os.Stat(openPath)
		if err != nil {
			return false
		}
	}

	// Step 1: size gate.
	if stat.Size() > rustMaxBinarySize {
		return false
	}

	f, err := elf.Open(openPath)
	if err != nil {
		return false
	}
	defer f.Close()

	// Step 2: .comment must contain "rustc".
	hasRustc, isStdRustc, commentData := checkRustComment(f)
	if !hasRustc {
		return false
	}

	// Step 3: reject hybrids with clang compilation units.
	if bytes.Contains(commentData, []byte("clang version")) {
		return false
	}

	// Step 4: if .symtab exists, the lang_start symbol is definitive.
	hasSymtab, hasLangStart := checkRustLangStart(f)
	if hasSymtab {
		return hasLangStart
	}

	// Step 5: stripped binary — fall back to rustc version format check.
	return isStdRustc
}

// stdRustcVersion matches official rustc release version strings embedded in
// ELF .comment sections. The format is strictly defined by the Rust release
// process and uses a 9-character commit hash:
//
//	stable:  "rustc version 1.91.0 (f8297e351 2025-10-28)"
//	beta:    "rustc version 1.92.0-beta.1 (abc123def 2025-11-01)"
//	nightly: "rustc version 1.93.0-nightly (def456abc 2025-11-15)"
//
// Vendor-forked compilers (used by browser projects for sandboxing and
// ABI-specific patches) deviate from this format — they use "-dev" suffixes,
// longer hashes, or extra build metadata. The regex intentionally rejects
// these so that stripped hybrid binaries don't pass detection.
var stdRustcVersion = regexp.MustCompile(`rustc version \d+\.\d+\.\d+(-nightly|-beta(\.\d+)?)?` +
	` \([0-9a-f]{9} \d{4}-\d{2}-\d{2}\)`)

// checkRustComment reads the ELF .comment section to determine if rustc was
// involved in producing this binary.
//
// Returns three values:
//   - hasRustc: true if the string "rustc" appears anywhere in .comment.
//   - isStdRustc: true if the rustc version string matches the official release
//     format. This distinguishes standard toolchain builds from vendor forks.
//   - rawData: the full .comment content, returned so callers can perform
//     additional checks (e.g. clang co-compiler detection) without re-reading.
//
// The .comment section is a concatenation of null-terminated strings, one per
// compilation unit. A typical Rust binary contains "GCC: (Ubuntu ...)" from
// libc and "rustc version X.Y.Z (...)" from rustc's LLVM codegen.
func checkRustComment(f *elf.File) (bool, bool, []byte) {
	section := f.Section(".comment")
	if section == nil || section.Size > rustMaxSectionSize {
		return false, false, nil
	}
	data, err := section.Data()
	if err != nil {
		return false, false, nil
	}
	if !bytes.Contains(data, []byte("rustc")) {
		return false, false, data
	}
	return true, stdRustcVersion.Match(data), data
}

// checkRustLangStart looks for the std::rt::lang_start symbol in the binary's
// string table. This is the most reliable indicator of a pure Rust binary
// because lang_start is the wrapper around fn main() that sets up the Rust
// runtime (panic handler, thread-local storage, etc.). It only exists when
// Rust owns the program's entry point.
//
// We scan .strtab as raw bytes rather than parsing .symtab symbol structs.
// Go's elf.File.Symbols() would allocate one Go struct per symbol — a typical
// Rust debug binary has 50K+ symbols, making struct-based iteration expensive.
// A single bytes.Contains on the string table achieves the same result with
// one allocation (the section data itself).
//
// Returns (symtabExists, hasLangStart). When symtabExists is false, the caller
// should fall back to alternative detection since the binary was stripped.
func checkRustLangStart(f *elf.File) (bool, bool) {
	symtab := f.SectionByType(elf.SHT_SYMTAB)
	if symtab == nil {
		return false, false
	}
	if symtab.Link >= uint32(len(f.Sections)) {
		return true, false
	}
	strtab := f.Sections[symtab.Link]
	if strtab.Size > rustMaxSectionSize {
		return true, false
	}
	data, err := strtab.Data()
	if err != nil {
		return true, false
	}

	// We search for the prefix "_ZN3std2rt10lang_start" which is the Itanium
	// ABI mangling of std::rt::lang_start. Two variants share this prefix:
	//
	//   Debug:   _ZN3std2rt10lang_start17h<hash>E
	//   Release: _ZN3std2rt10lang_start28_$u7b$$u7b$closure$u7d$$u7d$17h<hash>E
	//            (lang_start itself gets inlined, its closure survives)
	//
	// The internal helper (lang_start_internal) mangles differently because
	// Itanium ABI encodes name length as a decimal prefix: "10lang_start" vs
	// "19lang_start_internal". So our prefix match cannot accidentally hit the
	// internal variant, which is present in hybrid binaries.
	return true, bytes.Contains(data, []byte("_ZN3std2rt10lang_start"))
}

func extractEnvValue(environ string, key string) string {
	prefix := key + "="
	for _, entry := range strings.Split(environ, "\x00") {
		if v, ok := strings.CutPrefix(entry, prefix); ok {
			return v
		}
	}
	return ""
}
