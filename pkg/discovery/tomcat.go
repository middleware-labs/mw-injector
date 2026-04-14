// tomcat.go provides Tomcat-specific discovery helpers: scanning for Tomcat
// instances, extracting webapp names from webapps/ directories, and building
// ServiceSettings for Tomcat processes.
package discovery

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"
)

// TomcatInfo contains information about a Tomcat deployment
type TomcatInfo struct {
	IsTomcat      bool
	CatalinaBase  string
	CatalinaHome  string
	InstanceName  string
	Webapps       []string
	ServerXMLPath string
	Port          int
}

// detectTomcatDeployment checks if this is a Tomcat process and extracts info
func (d *discoverer) detectTomcatDeployment(cmdArgs []string) *TomcatInfo {
	tomcatInfo := &TomcatInfo{}

	isTomcat := false
	for _, arg := range cmdArgs {
		argLower := strings.ToLower(arg)
		if strings.Contains(argLower, "catalina") ||
			strings.Contains(argLower, "org.apache.catalina.startup.bootstrap") ||
			strings.Contains(argLower, "tomcat") {
			isTomcat = true
			break
		}
	}

	if !isTomcat {
		return tomcatInfo
	}

	tomcatInfo.IsTomcat = true

	for _, arg := range cmdArgs {
		if strings.HasPrefix(arg, "-Dcatalina.base=") {
			tomcatInfo.CatalinaBase = strings.TrimPrefix(arg, "-Dcatalina.base=")
		}
		if strings.HasPrefix(arg, "-Dcatalina.home=") {
			tomcatInfo.CatalinaHome = strings.TrimPrefix(arg, "-Dcatalina.home=")
		}
	}

	if tomcatInfo.CatalinaBase != "" {
		parts := strings.Split(tomcatInfo.CatalinaBase, "/")
		if len(parts) > 0 {
			tomcatInfo.InstanceName = parts[len(parts)-1]
		}
	}

	if tomcatInfo.CatalinaBase != "" {
		webapps := d.discoverTomcatWebapps(tomcatInfo.CatalinaBase)
		tomcatInfo.Webapps = webapps
	}

	if tomcatInfo.CatalinaBase != "" {
		tomcatInfo.ServerXMLPath = filepath.Join(tomcatInfo.CatalinaBase, "conf", "server.xml")

		port := d.extractTomcatPort(tomcatInfo.ServerXMLPath)
		if port > 0 {
			tomcatInfo.Port = port
		}
	}

	return tomcatInfo
}

func (d *discoverer) discoverTomcatWebapps(catalinaBase string) []string {
	webappsDir := filepath.Join(catalinaBase, "webapps")

	dirs, err := ioutil.ReadDir(webappsDir)
	if err != nil {
		return []string{}
	}

	var webapps []string
	for _, dir := range dirs {
		if dir.IsDir() {
			if dir.Name() != "ROOT" && dir.Name() != "manager" && dir.Name() != "host-manager" {
				webapps = append(webapps, dir.Name())
			}
		} else if strings.HasSuffix(dir.Name(), ".war") {
			webappName := strings.TrimSuffix(dir.Name(), ".war")
			if webappName != "ROOT" {
				webapps = append(webapps, webappName)
			}
		}
	}

	return webapps
}

func (d *discoverer) extractTomcatPort(serverXMLPath string) int {
	content, err := ioutil.ReadFile(serverXMLPath)
	if err != nil {
		return 0
	}

	re := regexp.MustCompile(`<Connector[^>]*port="(\d+)"[^>]*protocol="HTTP`)
	matches := re.FindSubmatch(content)

	if len(matches) > 1 {
		var port int
		fmt.Sscanf(string(matches[1]), "%d", &port)
		return port
	}

	return 0
}

// ExtractTomcatInfo returns Tomcat deployment information for a process.
func (p *Process) ExtractTomcatInfo() *TomcatInfo {
	d := &discoverer{}
	return d.detectTomcatDeployment(p.CommandArgs)
}

// IsTomcat checks if this is a Tomcat process.
func (p *Process) IsTomcat() bool {
	return p.DetailBool(DetailIsTomcat)
}

// GetTomcatWebapps returns the list of deployed webapps.
func (p *Process) GetTomcatWebapps() []string {
	tomcatInfo := p.ExtractTomcatInfo()
	return tomcatInfo.Webapps
}
