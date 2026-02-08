package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	evdev "github.com/gvalkov/golang-evdev"
)

type Config struct {
	StepSize      string
	IncludeHDMI   bool
	DevicePath    string
	SearchKeyword string
	Verbose       bool
}

var cfg Config

func main() {
	// 1. Parameter Parsing
	flag.StringVar(&cfg.StepSize, "step", "2%", "Volume adjustment step")
	flag.BoolVar(&cfg.IncludeHDMI, "hdmi", false, "Include HDMI/DP/Digital outputs")
	flag.StringVar(&cfg.DevicePath, "device", "", "Manual input device path (bypasses search)")
	flag.StringVar(&cfg.SearchKeyword, "keyword", "Consumer Control", "Device name keyword for auto-discovery")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Enable verbose logging")
	flag.Parse()

	// 2. Resolve Device Path
	targetDevicePath := cfg.DevicePath
	if targetDevicePath == "" {
		var err error
		targetDevicePath, err = findDevicePath(cfg.SearchKeyword)
		if err != nil {
			log.Fatalf("[E] Auto-discovery failed: %v", err)
		}
		if cfg.Verbose {
			fmt.Printf("[I] Device identified: %s\n", targetDevicePath)
		}
	}

	// 3. Open Device
	dev, err := evdev.Open(targetDevicePath)
	if err != nil {
		log.Fatalf("[E] Failed to open device [%s]: %v", targetDevicePath, err)
	}

	// 4. Grab Device (Exclusive Access)
	// Prevents other apps from receiving keys to avoid conflicts and reduce latency
	err = dev.Grab()
	if err != nil {
		log.Fatalf("[E] Could not grab device: %v", err)
	}
	if cfg.Verbose {
		fmt.Println("Device grabbed. Press Ctrl+C to exit.")
	}

	// 5. Cleanup Handling
	setupSignalHandler(dev)
	defer dev.Release()

	// 6. Event Loop
	log.Println("Listening for events...")
	for {
		event, err := dev.ReadOne()
		if err != nil {
			log.Printf("[W] Device disconnected or read error: %v", err)
			break
		}

		// Handle Key Press (1) or Auto-Repeat (2)
		if event.Type == evdev.EV_KEY && event.Value == 1 {
			switch event.Code {
			case evdev.KEY_VOLUMEUP:
				changeVolume("+" + cfg.StepSize)
			case evdev.KEY_VOLUMEDOWN:
				changeVolume("-" + cfg.StepSize)
			case evdev.KEY_MUTE:
				handleMuteLogic()
			}
		}
	}
}

func setupSignalHandler(dev *evdev.InputDevice) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		if cfg.Verbose {
			fmt.Println("\nReleasing device and exiting...")
		}
		dev.Release()
		os.Exit(0)
	}()
}

func handleMuteLogic() {
	sinks, currentDefault, err := getFilteredSinks()
	if err != nil {
		log.Printf("Audio device error: %v", err)
		return
	}

	if len(sinks) <= 1 {
		// Single device: Toggle mute state
		if cfg.Verbose {
			log.Println("Single device mode: Toggling mute")
		}
		exec.Command("wpctl", "set-mute", "@DEFAULT_AUDIO_SINK@", "toggle").Run()
		showNotification("Mute Toggled", "")
	} else {
		// Multiple devices: Cycle to next sink
		switchToNextDevice(sinks, currentDefault)
	}
}

func changeVolume(amount string) {
	// Reformat "+2%" to "2%+" for wpctl compatibility
	val := strings.TrimLeft(amount, "+-")
	op := "+"
	if strings.HasPrefix(amount, "-") {
		op = "-"
	}

	err := exec.Command("wpctl", "set-volume", "@DEFAULT_AUDIO_SINK@", val+op).Run()
	if err != nil && cfg.Verbose {
		log.Printf("Volume adjustment failed: %v", err)
	}
}

func switchToNextDevice(sinks []string, currentDefault string) {
	nextIdx := 0
	found := false
	for i, name := range sinks {
		if name == currentDefault {
			nextIdx = (i + 1) % len(sinks)
			found = true
			break
		}
	}
	if !found {
		nextIdx = 0
	}

	nextSink := sinks[nextIdx]

	// 1. Set default 2. Unmute
	exec.Command("pactl", "set-default-sink", nextSink).Run()
	exec.Command("pactl", "set-sink-mute", nextSink, "0").Run()

	if cfg.Verbose {
		fmt.Printf("Switched to: %s\n", nextSink)
	}
	showNotification("Audio Output Switched", fmt.Sprintf("Active: %s", nextSink))
}

func getFilteredSinks() ([]string, string, error) {
	// Get current default
	defaultOut, err := exec.Command("pactl", "get-default-sink").Output()
	if err != nil {
		return nil, "", err
	}
	currentDefault := strings.TrimSpace(string(defaultOut))

	// Get sink list
	out, err := exec.Command("pactl", "list", "short", "sinks").Output()
	if err != nil {
		return nil, "", err
	}

	var sinks []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	excludeKeywords := []string{"hdmi", "digital-video", "spdif"}

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		name := fields[1]

		if !cfg.IncludeHDMI {
			isExcluded := false
			lowerName := strings.ToLower(name)
			for _, kw := range excludeKeywords {
				if strings.Contains(lowerName, kw) {
					isExcluded = true
					break
				}
			}
			if isExcluded {
				continue
			}
		}
		sinks = append(sinks, name)
	}
	return sinks, currentDefault, nil
}

func findDevicePath(keyword string) (string, error) {
	devices, err := evdev.ListInputDevices()
	if err != nil {
		return "", err
	}

	keywordLower := strings.ToLower(keyword)
	for _, dev := range devices {
		if strings.Contains(strings.ToLower(dev.Name), keywordLower) {
			return dev.Fn, nil
		}
	}
	return "", fmt.Errorf("no device found matching '%s'", keyword)
}

func showNotification(title, body string) {
	// Async notification to prevent UI lag
	go exec.Command("notify-send", "-t", "2000", "-h", "string:x-canonical-private-synchronous:volume-switch", title, body).Run()
}