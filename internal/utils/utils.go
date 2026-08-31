package utils

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// PromptYesNo prompts the user for a yes/no answer
func PromptYesNo(prompt string) bool {
	fmt.Printf("%s (y/n): ", prompt)

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		response := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return response == "y" || response == "yes"
	}

	return false
}

// SelectOption presents options and returns the user's selection
func SelectOption(prompt string, options []string) string {
	if len(options) == 0 {
		return ""
	}

	if len(options) == 1 {
		return options[0]
	}

	fmt.Printf("%s:\n", prompt)
	for i, option := range options {
		fmt.Printf("%d. %s\n", i+1, option)
	}

	for {
		fmt.Print("Choice: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			input := strings.TrimSpace(scanner.Text())

			// Try to parse as number
			if choice, err := strconv.Atoi(input); err == nil {
				if choice >= 1 && choice <= len(options) {
					return options[choice-1]
				}
			}

			// Check if input matches an option
			for _, option := range options {
				if strings.EqualFold(input, option) {
					return option
				}
			}
		}

		fmt.Println("Invalid choice. Please try again.")
	}
}

// FileExists checks if a file exists
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// DirectoryExists checks if a directory exists
func DirectoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// CopyFile copies a file from source to destination
func CopyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	// Copy file permissions
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("failed to get source file info: %w", err)
	}

	if err := os.Chmod(dst, sourceInfo.Mode()); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	return nil
}

// OpenURL opens a URL in the default browser
func OpenURL(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Start()
}

// BoolStatus returns a colored status indicator based on the boolean value
func BoolStatus(ok bool) string {
	if ok {
		return Colorize("[OK]", ColorGreen)
	}
	return Colorize("[FAIL]", ColorRed)
}

// FormatStatus formats a status string with appropriate color
func FormatStatus(status string) string {
	switch status {
	case "running":
		return Colorize("[RUNNING]", ColorGreen) + " running"
	case "stopped", "exited":
		return Colorize("[STOPPED]", ColorRed) + " stopped"
	case "starting":
		return Colorize("[STARTING]", ColorCyan) + " starting"
	case "stopping":
		return Colorize("[STOPPING]", ColorYellow) + " stopping"
	case "not initialized", "not found":
		return Colorize("[ERROR]", ColorRed) + " " + status
	default:
		return Colorize("[WARNING]", ColorYellow) + " " + status
	}
}

// CleanupDNS removes DNS resolver configuration
func CleanupDNS() error {
	switch runtime.GOOS {
	case "darwin":
		return cleanupDNSMacOS()
	case "linux":
		return cleanupDNSLinux()
	default:
		return nil
	}
}

// cleanupDNSMacOS removes DNS resolver configuration on macOS
func cleanupDNSMacOS() error {
	// Remove resolver file
	if err := exec.Command("sudo", "rm", "-f", "/etc/resolver/test").Run(); err != nil {
		// Ignore error if file doesn't exist
		if !strings.Contains(err.Error(), "No such file") {
			return fmt.Errorf("failed to remove resolver file: %w", err)
		}
	}

	// Flush DNS cache
	exec.Command("sudo", "dscacheutil", "-flushcache").Run()
	exec.Command("sudo", "killall", "-HUP", "mDNSResponder").Run()

	return nil
}

// cleanupDNSLinux removes DNS resolver configuration on Linux
func cleanupDNSLinux() error {
	// Remove systemd-resolved configuration if it exists
	if CommandExists("resolvectl") || CommandExists("systemd-resolve") {
		// Remove the DNS configuration drop-in file
		exec.Command("sudo", "rm", "-f", "/etc/systemd/resolved.conf.d/alios.conf").Run()

		// Restart systemd-resolved
		exec.Command("sudo", "systemctl", "restart", "systemd-resolved").Run()
	}

	// Remove dnsmasq configuration if it exists
	if FileExists("/etc/dnsmasq.d/alios.conf") {
		exec.Command("sudo", "rm", "-f", "/etc/dnsmasq.d/alios.conf").Run()
		if CommandExists("systemctl") {
			exec.Command("sudo", "systemctl", "restart", "dnsmasq").Run()
		} else {
			exec.Command("sudo", "service", "dnsmasq", "restart").Run()
		}
	}

	// Flush DNS cache
	if CommandExists("resolvectl") {
		exec.Command("sudo", "resolvectl", "flush-caches").Run()
	} else if CommandExists("systemd-resolve") {
		exec.Command("sudo", "systemd-resolve", "--flush-caches").Run()
	}

	return nil
}

// SetupTestDomainResolver sets up DNS resolver for .test domains
func SetupTestDomainResolver() error {
	switch runtime.GOOS {
	case "darwin":
		return setupTestDomainResolverMacOS()
	case "linux":
		return setupTestDomainResolverLinux()
	default:
		return nil
	}
}

// setupTestDomainResolverMacOS sets up DNS resolver for .test domains on macOS
func setupTestDomainResolverMacOS() error {
	resolverFile := "/etc/resolver/test"
	content := "port 5354\nnameserver 127.0.0.1\n"

	// Check if already configured correctly
	if data, err := os.ReadFile(resolverFile); err == nil && string(data) == content {
		return nil // Already configured
	}

	// Create resolver directory
	exec.Command("sudo", "mkdir", "-p", "/etc/resolver").Run()

	// Write resolver configuration
	cmd := exec.Command("sudo", "tee", resolverFile)
	cmd.Stdin = strings.NewReader(content)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to setup resolver: %w", err)
	}

	// Flush DNS cache
	exec.Command("sudo", "dscacheutil", "-flushcache").Run()
	exec.Command("sudo", "killall", "-HUP", "mDNSResponder").Run()

	return nil
}

// setupTestDomainResolverLinux sets up DNS resolver for .test domains on Linux
func setupTestDomainResolverLinux() error {
	// Try systemd-resolved first (most modern Linux distributions)
	if CommandExists("resolvectl") || CommandExists("systemd-resolve") {
		return setupSystemdResolved()
	}

	// Try dnsmasq if available
	if CommandExists("dnsmasq") || FileExists("/etc/dnsmasq.conf") {
		return setupDnsmasq()
	}

	// If neither is available, provide helpful instructions
	PrintWarning("Could not automatically configure DNS for .test domains")
	fmt.Println("\nTo manually configure DNS for .test domains, add this to your DNS configuration:")
	fmt.Println("  Domain: .test")
	fmt.Println("  DNS Server: 127.0.0.1:5354")
	fmt.Println("\nAlternatively, you can add entries to /etc/hosts for each project:")
	fmt.Println("  127.0.0.1  myproject.test")

	return nil
}

// setupSystemdResolved configures systemd-resolved for .test domains
func setupSystemdResolved() error {
	// Create resolved.conf.d directory if it doesn't exist
	if err := exec.Command("sudo", "mkdir", "-p", "/etc/systemd/resolved.conf.d").Run(); err != nil {
		return fmt.Errorf("failed to create resolved.conf.d directory: %w", err)
	}

	// Create configuration for .test domain
	config := `[Resolve]
DNS=127.0.0.1:5354
Domains=~test
`

	// Write configuration file
	cmd := exec.Command("sudo", "tee", "/etc/systemd/resolved.conf.d/alios.conf")
	cmd.Stdin = strings.NewReader(config)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to write resolved configuration: %w", err)
	}

	// Restart systemd-resolved
	if err := exec.Command("sudo", "systemctl", "restart", "systemd-resolved").Run(); err != nil {
		return fmt.Errorf("failed to restart systemd-resolved: %w", err)
	}

	// Flush DNS cache
	if CommandExists("resolvectl") {
		exec.Command("sudo", "resolvectl", "flush-caches").Run()
	} else if CommandExists("systemd-resolve") {
		exec.Command("sudo", "systemd-resolve", "--flush-caches").Run()
	}

	return nil
}

// setupDnsmasq configures dnsmasq for .test domains
func setupDnsmasq() error {
	// Create dnsmasq.d directory if it doesn't exist
	if err := exec.Command("sudo", "mkdir", "-p", "/etc/dnsmasq.d").Run(); err != nil {
		return fmt.Errorf("failed to create dnsmasq.d directory: %w", err)
	}

	// Create configuration for .test domain
	config := "server=/test/127.0.0.1#5354\n"

	// Write configuration file
	cmd := exec.Command("sudo", "tee", "/etc/dnsmasq.d/alios.conf")
	cmd.Stdin = strings.NewReader(config)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to write dnsmasq configuration: %w", err)
	}

	// Restart dnsmasq
	if CommandExists("systemctl") {
		if err := exec.Command("sudo", "systemctl", "restart", "dnsmasq").Run(); err != nil {
			return fmt.Errorf("failed to restart dnsmasq: %w", err)
		}
	} else {
		if err := exec.Command("sudo", "service", "dnsmasq", "restart").Run(); err != nil {
			return fmt.Errorf("failed to restart dnsmasq: %w", err)
		}
	}

	return nil
}

// EnsureDirectory creates a directory if it doesn't exist
func EnsureDirectory(path string) error {
	if DirectoryExists(path) {
		return nil
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}

	return nil
}

// GetInput prompts the user for input and returns the trimmed response
func GetInput(prompt string) string {
	fmt.Print(prompt)

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}

	return ""
}

// RunCommand executes a command and returns the output
func RunCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command failed: %w\nOutput: %s", err, string(output))
	}

	return strings.TrimSpace(string(output)), nil
}

// RunCommandSilent executes a command silently (no output)
func RunCommandSilent(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// CommandExists checks if a command exists in PATH
func CommandExists(command string) bool {
	_, err := exec.LookPath(command)
	return err == nil
}

// IsRoot checks if the program is running with root privileges
func IsRoot() bool {
	if runtime.GOOS == "windows" {
		// Windows doesn't have the same concept of root
		// Check if running as administrator would require Windows-specific code
		return false
	}

	return os.Geteuid() == 0
}

// GetEnvDefault returns an environment variable value or a default
func GetEnvDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// StringSliceContains checks if a string slice contains a value
func StringSliceContains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}

// RemoveFromStringSlice removes a value from a string slice
func RemoveFromStringSlice(slice []string, value string) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != value {
			result = append(result, item)
		}
	}
	return result
}

// TruncateString truncates a string to a maximum length
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	if maxLen <= 3 {
		return s[:maxLen]
	}

	return s[:maxLen-3] + "..."
}

// PadString pads a string to a specific length
func PadString(s string, length int) string {
	if len(s) >= length {
		return s
	}

	return s + strings.Repeat(" ", length-len(s))
}

// CenterString centers a string within a specific width
func CenterString(s string, width int) string {
	if len(s) >= width {
		return s
	}

	leftPad := (width - len(s)) / 2
	rightPad := width - len(s) - leftPad

	return strings.Repeat(" ", leftPad) + s + strings.Repeat(" ", rightPad)
}

// ColorString returns a colored string (ANSI escape codes)
type Color string

const (
	ColorReset  Color = "\033[0m"
	ColorRed    Color = "\033[31m"
	ColorGreen  Color = "\033[32m"
	ColorYellow Color = "\033[33m"
	ColorBlue   Color = "\033[34m"
	ColorPurple Color = "\033[35m"
	ColorCyan   Color = "\033[36m"
	ColorGray   Color = "\033[37m"
	ColorWhite  Color = "\033[97m"
)

// Colorize adds color to a string
func Colorize(s string, color Color) string {
	if runtime.GOOS == "windows" {
		// Windows terminal might not support ANSI colors
		return s
	}

	return string(color) + s + string(ColorReset)
}

// PrintSuccess prints a success message with green color
func PrintSuccess(message string) {
	fmt.Println(Colorize("[SUCCESS]", ColorGreen) + " " + message)
}

// PrintError prints an error message with red color
func PrintError(message string) {
	fmt.Fprintln(os.Stderr, Colorize("[ERROR]", ColorRed)+" "+message)
}

// PrintWarning prints a warning message with yellow color
func PrintWarning(message string) {
	fmt.Println(Colorize("[WARNING]", ColorYellow) + " " + message)
}

// PrintInfo prints an info message with blue color
func PrintInfo(message string) {
	fmt.Println(Colorize("[INFO]", ColorBlue) + " " + message)
}

// PrintStep prints a step message with cyan color
func PrintStep(message string) {
	fmt.Println(Colorize("[*]", ColorCyan) + " " + message)
}

// PrintStatus prints a status message with white color
func PrintStatus(message string) {
	fmt.Println(Colorize("[-]", ColorWhite) + " " + message)
}

// Spinner provides a simple loading spinner
type Spinner struct {
	chars []string
	index int
}

// NewSpinner creates a new spinner
func NewSpinner() *Spinner {
	return &Spinner{
		chars: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		index: 0,
	}
}

// Next returns the next spinner character
func (s *Spinner) Next() string {
	char := s.chars[s.index]
	s.index = (s.index + 1) % len(s.chars)
	return char
}

// ProgressBar provides a simple progress bar
type ProgressBar struct {
	total   int
	current int
	width   int
}

// NewProgressBar creates a new progress bar
func NewProgressBar(total int, width int) *ProgressBar {
	return &ProgressBar{
		total:   total,
		current: 0,
		width:   width,
	}
}

// Update updates the progress bar
func (p *ProgressBar) Update(current int) {
	p.current = current
}

// Render renders the progress bar as a string
func (p *ProgressBar) Render() string {
	if p.total == 0 {
		return ""
	}

	percentage := float64(p.current) / float64(p.total)
	filled := int(percentage * float64(p.width))

	bar := strings.Repeat("█", filled) + strings.Repeat("░", p.width-filled)

	return fmt.Sprintf("[%s] %.0f%%", bar, percentage*100)
}

// Increment increments the progress bar by 1
func (p *ProgressBar) Increment() {
	p.current++
}

// TableWriter provides simple table formatting
type TableWriter struct {
	headers []string
	rows    [][]string
}

// NewTableWriter creates a new table writer
func NewTableWriter(headers ...string) *TableWriter {
	return &TableWriter{
		headers: headers,
		rows:    make([][]string, 0),
	}
}

// AddRow adds a row to the table
func (t *TableWriter) AddRow(row ...string) {
	t.rows = append(t.rows, row)
}

// Render renders the table as a string
func (t *TableWriter) Render() string {
	if len(t.headers) == 0 {
		return ""
	}

	// Calculate column widths
	widths := make([]int, len(t.headers))
	for i, header := range t.headers {
		widths[i] = len(header)
	}

	for _, row := range t.rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	var result strings.Builder

	// Render headers
	for i, header := range t.headers {
		result.WriteString(PadString(header, widths[i]))
		if i < len(t.headers)-1 {
			result.WriteString("  ")
		}
	}
	result.WriteString("\n")

	// Render separator
	for i, width := range widths {
		result.WriteString(strings.Repeat("-", width))
		if i < len(widths)-1 {
			result.WriteString("  ")
		}
	}
	result.WriteString("\n")

	// Render rows
	for _, row := range t.rows {
		for i, cell := range row {
			if i < len(widths) {
				result.WriteString(PadString(cell, widths[i]))
				if i < len(row)-1 {
					result.WriteString("  ")
				}
			}
		}
		result.WriteString("\n")
	}

	return result.String()
}
