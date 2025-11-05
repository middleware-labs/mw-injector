# MW Injector 🚀

**Zero-configuration Java application instrumentation that actually works.**

MW Injector automatically discovers, instruments, and manages Java applications across your entire infrastructure with a single command.

## ⚡ Quick Start

```bash
# List all Java processes
sudo mw-injector list-all

# Auto-instrument everything (host processes)
sudo mw-injector auto-instrument

# Auto-instrument Docker containers
sudo mw-injector instrument-docker

# List instrumented containers
sudo mw-injector list-docker
```

# Config-based mode (no prompts, uses saved config)
```
sudo mw-injector auto-instrument-config
```

# Auto-instrument Docker containers
```
sudo mw-injector instrument-docker-config
```
## 🔧 Configuration File

Create a config file to avoid repetitive API key prompts and enable fully automated instrumentation:
```bash
# Create system-wide config
sudo tee /etc/mw-injector.conf << EOF
MW_API_KEY=your_middleware_api_key_here
MW_TARGET=https://prod.middleware.io:443
MW_JAVA_AGENT_PATH=/opt/middleware/agents/middleware-javaagent-1.8.1.jar
EOF
```

That's it. Your Java apps are now sending telemetry data to Middleware.io.

## 🎯 What Makes This Different

- **Auto-Discovery**: Finds Java processes everywhere - host, Docker, Docker Compose, systemd services
- **Zero Configuration**: No manual agent setup, no classpath hell, no environment variable gymnastics
- **Intelligent Detection**: Recognizes Tomcat instances, Spring Boot apps, JAR files, and service types
- **Permission-Aware**: Handles user contexts, systemd security, and file access automatically
- **Reversible**: Clean uninstrumentation that restores original state
- **Production-Ready**: Designed for enterprise environments with proper error handling

## 🔥 Core Capabilities

### Process Discovery
```bash
Found 3 Java processes:

PID: 1234
  Service: user-auth-service
  Owner: appuser
  Agent: ❌ None
  Type: Spring Boot
  Config: ❌ Not configured

PID: 5678
  Service: tomcat-ecommerce
  Owner: tomcat
  Agent: ✅ MW
  Type: Tomcat
  Instance: ecommerce
  Webapps: [api, admin, shop]
  Config: ✅ /etc/middleware/tomcat/tomcat-ecommerce.conf
```

### Docker Integration
```bash
Found 2 Java Docker containers:

Container: payment-service
  Image: openjdk:11-jre-slim
  Agent: ❌ Not instrumented
  Type: Docker Compose
  Project: microservices
  Service: payment

Container: legacy-app
  Image: tomcat:9.0
  Agent: ✅ Instrumented
  JAR Files: [legacy-app.war]
```

### Tomcat Support
- Automatically detects Tomcat instances and webapps
- Supports multiple Tomcat deployments per host
- Handles CATALINA_OPTS integration
- Per-webapp service naming with context expansion

### Systemd Integration
- Creates proper systemd drop-in files
- Manages service restarts automatically
- Handles permission contexts and security policies
- Supports both standard Java services and Tomcat

## 🛠 Installation

```bash
# Build the project (Yeah yeah release pipeline will be coming soon) 
go build -o mw-injector ./cmd/mw-injector

# Make executable
chmod +x mw-injector

# Move to PATH
sudo mv mw-injector /usr/local/bin/

```

## 📋 Requirements

- Linux (systemd-based distributions)
- Root privileges (for system-wide instrumentation)
- Docker (optional, for container instrumentation)
- Middleware.io account and API key

## 🎮 Usage Examples

### Basic Workflow
```bash
# 1. See what's running
sudo mw-injector list-all

# 2. Auto-instrument everything
sudo mw-injector auto-instrument
# Enter your Middleware.io API key when prompted

# 3. Verify instrumentation
sudo mw-injector list

# 4. Check your Middleware.io dashboard
# 🎉 Data should be flowing
```

### Docker Containers
```bash
# Instrument all Java containers
sudo mw-injector instrument-docker

# Instrument specific container
sudo mw-injector instrument-container my-java-app

# Remove instrumentation
sudo mw-injector uninstrument-docker
```

### Cleanup
```bash
# Remove all instrumentation
sudo mw-injector uninstrument

# Remove Docker instrumentation
sudo mw-injector uninstrument-docker
```

## 🏗 Architecture

MW Injector is built with a modular architecture:

- **Agent Management**: Handles Java agent installation and permissions
- **Process Discovery**: Finds and analyzes Java processes across the system
- **Service Naming**: Generates intelligent service names from process context
- **Systemd Integration**: Manages service configuration and restarts
- **State Management**: Tracks instrumentation state and handles cleanup

## 🤝 Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details.

## 📄 License

MIT License - see [LICENSE](LICENSE) file for details.

## 🆘 Support

- 📖 [Documentation](docs/)
- 🐛 [Issue Tracker](https://github.com/your-org/mw-injector/issues)
- 💬 [Discussions](https://github.com/your-org/mw-injector/discussions)
- 📧 [Email Support](mailto:support@middleware.io)

---

**Built with ❤️  and way too much nicotine on sleepless nights**

*Making Java instrumentation suck less, one process at a time.*
