package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fatih/color"
)

// LogcatOptions holds configuration for filtering logcat output
type LogcatOptions struct {
	Filters   []string
	Tag       string
	Level     string
	Device    string        // Serial number of the device/emulator
	MaxDelta  time.Duration // Maximum duration for showing time differences
	KeepGoing bool          // Whether to restart the command when it exits
}

// LogLevelColors maps log levels to color functions
var LogLevelColors = map[string]func(format string, a ...any) string{
	"V": color.New(color.FgWhite).SprintfFunc(),   // Verbose: White
	"D": color.New(color.FgBlue).SprintfFunc(),    // Debug: Blue
	"I": color.New(color.FgGreen).SprintfFunc(),   // Info: Green
	"W": color.New(color.FgYellow).SprintfFunc(),  // Warning: Yellow
	"E": color.New(color.FgRed).SprintfFunc(),     // Error: Red
	"F": color.New(color.FgMagenta).SprintfFunc(), // Fatal: Magenta
}

// TagColor is the color function for tags
var TagColor = color.New(color.FgBlack, color.BgCyan).SprintfFunc()

// lastTagTime tracks the last timestamp for each tag
var lastTagTime = make(map[string]time.Time)

func main() {
	// Parse command-line arguments for filtering
	opts := parseArgs()

	for {
		// Start adb logcat command
		cmd := buildAdbCommand(opts)

		// Set up pipe for command output
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			fmt.Fprint(os.Stderr, LogLevelColors["E"]("Error creating stdout pipe: %v\n", err))
			os.Exit(1)
		}

		// Start the command
		if err := cmd.Start(); err != nil {
			fmt.Fprint(os.Stderr, LogLevelColors["E"]("Error starting adb logcat: %v\n", err))
			os.Exit(1)
		}

		lastTag := ""
		lastTime := time.Time{}
		lastOther := ""

		// Read and display logs in real-time
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			lastTag, lastTime, lastOther = printColoredLog(line, lastTag, lastTime, lastOther, opts)
		}

		// Check for errors while scanning
		if err := scanner.Err(); err != nil {
			fmt.Fprint(os.Stderr, LogLevelColors["E"]("Error reading logcat output: %v\n", err))
		}

		// Wait for the command to finish
		if err := cmd.Wait(); err != nil {
			fmt.Fprint(os.Stderr, LogLevelColors["E"]("Error waiting for adb logcat: %v\n", err))
		}

		// Exit if keep-going is not enabled
		if !opts.KeepGoing {
			return
		}

		// Add a small delay before restarting to prevent rapid restart loops
		time.Sleep(time.Second)
		fmt.Fprintf(os.Stderr, "adb logcat exited, restarting...\n")
	}
}

// parseArgs parses command-line arguments for filtering options
func parseArgs() LogcatOptions {
	opts := LogcatOptions{}

	fs := flag.NewFlagSet("logcatcolor", flag.ExitOnError)
	// Define flags
	var filters []string
	fs.Func("s", "Filter string to match in log messages (can be specified multiple times)", func(s string) error {
		filters = append(filters, s)
		return nil
	})
	tag := fs.String("t", "", "Filter by tag")
	level := fs.String("l", "", "Filter by log level (V/D/I/W/E/F)")
	device := fs.String("d", "", "Device serial number or -d for hardware device")
	emulator := fs.Bool("e", false, "Use default emulator device")
	maxDelta := fs.Duration("delta", 10*time.Second, "Maximum duration for showing time differences between log entries")
	keepGoing := fs.Bool("k", false, "Restart the command when it exits")

	// Filter os.Args[1:] to remove "-d" if the next argument starts with "-"
	// This prevents flag.Parse from incorrectly interpreting a subsequent flag as the value for -d.
	originalCmdArgs := os.Args[1:]
	filteredCmdArgs := make([]string, 0, len(originalCmdArgs))
	for i, arg := range originalCmdArgs {
		isDashD := arg == "-d" || arg == "--d" || strings.HasPrefix(arg, "-d=") || strings.HasPrefix(arg, "--d=")
		if isDashD {
			if i+1 == len(originalCmdArgs) {
				opts.Device = "-d"
				continue
			} else if i+1 < len(originalCmdArgs) {
				nextArg := originalCmdArgs[i+1]
				if strings.HasPrefix(nextArg, "-") {
					opts.Device = "-d"
					continue
				}
			}
		}
		filteredCmdArgs = append(filteredCmdArgs, arg)
	}

	// Parse flags
	fs.Parse(filteredCmdArgs)

	// Set options from flags
	opts.Filters = filters
	opts.Tag = *tag
	opts.Level = strings.ToUpper(*level)
	opts.MaxDelta = *maxDelta
	opts.KeepGoing = *keepGoing

	// Handle device selection
	switch {
	case *emulator:
		opts.Device = "-e"
	case *device != "":
		opts.Device = *device
	}

	return opts
}

// buildAdbCommand constructs the adb logcat command with filters
func buildAdbCommand(opts LogcatOptions) *exec.Cmd {
	args := []string{"logcat", "-v", "threadtime"}

	// Add device selection if specified
	switch opts.Device {
	case "-d":
		args = append([]string{"-d"}, args...)
	case "-e":
		args = append([]string{"-e"}, args...)
	default:
		args = append([]string{"-s", opts.Device}, args...)
	}

	// Add filters if specified
	if opts.Tag != "" && opts.Level != "" {
		args = append(args, fmt.Sprintf("%s:%s", opts.Tag, opts.Level))
	}

	for _, filter := range opts.Filters {
		args = append(args, "-s", filter)
	}

	return exec.Command("adb", args...)
}

// parseTimestamp parses the timestamp from a log line
func parseTimestamp(line string) (time.Time, error) {
	// Format: MM-DD HH:MM:SS.mmm
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid timestamp format")
	}
	timestamp := parts[0] + " " + parts[1]
	return time.Parse("01-02 15:04:05.000", timestamp)
}

// findFieldIndices returns the indices of the first non-space character for each field
// up to the specified maximum number of fields
func findFieldIndices(line string, maxFields int) []int {
	indices := make([]int, 0, maxFields)
	inField := false

	for i, char := range line {
		if char != ' ' && !inField {
			// Found start of a new field
			indices = append(indices, i)
			inField = true
			if len(indices) >= maxFields {
				break
			}
		} else if char == ' ' {
			inField = false
		}
	}

	return indices
}

// printColoredLog prints a log line with color based on its log level
func printColoredLog(line, lastTag string, lastTime time.Time, lastOther string, opts LogcatOptions) (string, time.Time, string) {
	// New logcat line format: [MM-DD HH:MM:SS.mmm PID TID LEVEL TAG: MESSAGE]
	// Example: "04-19 19:34:18.813  5587  5708 I artd    : GetBestInfo no usable artifacts"
	parts := findFieldIndices(line, 6)
	if len(parts) < 6 {
		// Fallback to default if line format is unexpected
		fmt.Println(line)
		return lastTag, lastTime, lastOther
	}

	levelIndex := parts[4]
	level := line[levelIndex : levelIndex+1]

	// Get the color function for the log level, default to no color if not found
	colorFunc, exists := LogLevelColors[level]
	if !exists {
		fmt.Println(line)
		return lastTag, lastTime, lastOther
	}

	tagIndex := parts[5]
	colonIndex := strings.IndexRune(line[tagIndex:], ':')
	if colonIndex == -1 {
		fmt.Println(line)
		return lastTag, lastTime, lastOther
	}
	colonIndex += tagIndex

	tag := strings.TrimSpace(line[tagIndex:colonIndex])
	tagSpace := line[tagIndex+len(tag) : colonIndex]

	// Parse current timestamp
	currentTime, err := parseTimestamp(line)
	if err != nil {
		fmt.Println(line)
		return lastTag, lastTime, lastOther
	}

	other := line[:parts[1]] + line[parts[2]:parts[4]]

	// Calculate delta time
	delta := currentTime.Sub(lastTime)

	// Prepare metadata part
	var metadata string
	if lastTag == tag && delta.Seconds() < opts.MaxDelta.Seconds() {
		metadata = fmt.Sprintf("%-*v", levelIndex, "+"+delta.String())
	} else {
		// Use original metadata for first occurrence
		metadata = line[:levelIndex]
		lastTime = currentTime
		lastOther = other
	}

	message := line[colonIndex+2:]

	fmt.Printf("%s%s %s%s : %s\n", metadata, colorFunc("%s", level), TagColor("%s", tag), tagSpace, colorFunc("%s", message))

	return tag, lastTime, lastOther
}
