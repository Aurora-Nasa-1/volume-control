package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	evdev "github.com/gvalkov/golang-evdev"
)

type ControlMode int

const (
	ModeAudio ControlMode = iota
	ModeBrightness
)

type Config struct {
	StepSize      string
	IncludeHDMI   bool
	DevicePath    string
	SearchKeyword string
	Verbose       bool
}

var (
	cfg         Config
	currentMode = ModeAudio
	
	muteTimer *time.Timer
	mu        sync.Mutex

	brightnessDelta int32
	brightnessCh    = make(chan struct{}, 1)
	
	useDDC     bool
	ddcBusNum  string
)

func main() {
	flag.StringVar(&cfg.StepSize, "step", "2%", "Adjustment step")
	flag.BoolVar(&cfg.IncludeHDMI, "hdmi", false, "Include HDMI/DP outputs")
	flag.StringVar(&cfg.DevicePath, "device", "", "Device path")
	flag.StringVar(&cfg.SearchKeyword, "keyword", "Consumer Control", "Search keyword")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Verbose logging")
	flag.Parse()

	detectBrightnessMode()
	go brightnessWorker()

	target := cfg.DevicePath
	if target == "" {
		var err error
		target, err = findDevicePath(cfg.SearchKeyword)
		if err != nil {
			log.Fatalf("[E] Device not found: %v", err)
		}
		if cfg.Verbose {
			fmt.Printf("[I] Device: %s\n", target)
		}
	}

	dev, err := evdev.Open(target)
	if err != nil {
		log.Fatalf("[E] Open failed: %v", err)
	}

	if err = dev.Grab(); err != nil {
		log.Fatalf("[E] Grab failed: %v", err)
	}

	setupSignalHandler(dev)
	defer dev.Release()

	log.Println("Started. Double-click Mute to switch modes.")

	for {
		event, err := dev.ReadOne()
		if err != nil {
			break
		}

		if event.Type == evdev.EV_KEY && (event.Value == 1 || event.Value == 2) {
			switch event.Code {
			case evdev.KEY_VOLUMEUP:
				handleAdjustment("up")
			case evdev.KEY_VOLUMEDOWN:
				handleAdjustment("down")
			case evdev.KEY_MUTE:
				if event.Value == 1 {
					handleMutePress()
				}
			}
		}
	}
}

func detectBrightnessMode() {
	// Prefer native backlight
	entries, _ := os.ReadDir("/sys/class/backlight")
	if len(entries) > 0 {
		useDDC = false
		if cfg.Verbose {
			log.Println("[I] Mode: Laptop Backlight")
		}
		return
	}

	useDDC = true
	// Optimize DDC: find bus once
	out, err := exec.Command("ddcutil", "detect", "--terse").Output()
	if err == nil {
		re := regexp.MustCompile(`/dev/i2c-(\d+)`)
		match := re.FindSubmatch(out)
		if len(match) > 1 {
			ddcBusNum = string(match[1])
			if cfg.Verbose {
				log.Printf("[I] Mode: DDC/CI (Bus %s)", ddcBusNum)
			}
		}
	}
	if ddcBusNum == "" && cfg.Verbose {
		log.Println("[I] Mode: DDC/CI (Auto-scan)")
	}
}

func handleAdjustment(dir string) {
	prefix := "+"
	if dir == "down" {
		prefix = "-"
	}

	if currentMode == ModeAudio {
		changeVolume(prefix + cfg.StepSize)
	} else {
		changeBrightness(prefix + cfg.StepSize)
	}
}

func handleMutePress() {
	mu.Lock()
	defer mu.Unlock()

	if muteTimer != nil {
		if muteTimer.Stop() {
			toggleControlMode()
		}
		muteTimer = nil
	} else {
		muteTimer = time.AfterFunc(300*time.Millisecond, func() {
			mu.Lock()
			defer mu.Unlock()
			handleMuteLogic()
			muteTimer = nil
		})
	}
}

func toggleControlMode() {
	if currentMode == ModeAudio {
		currentMode = ModeBrightness
		modeStr := "Backlight"
		if useDDC {
			modeStr = "DDC/CI"
		}
		showNotification("Mode: Brightness", fmt.Sprintf("Control: %s", modeStr))
	} else {
		currentMode = ModeAudio
		showNotification("Mode: Audio", "Control: System Volume")
	}
}

func changeBrightness(amt string) {
	s := strings.TrimRight(amt, "%")
	val, _ := strconv.Atoi(s)
	atomic.AddInt32(&brightnessDelta, int32(val))

	select {
	case brightnessCh <- struct{}{}:
	default:
	}
}

func brightnessWorker() {
	for range brightnessCh {
		for {
			delta := atomic.SwapInt32(&brightnessDelta, 0)
			if delta == 0 {
				break
			}
			
			op := "+"
			absVal := delta
			if delta < 0 {
				op = "-"
				absVal = -delta
			}
			valStr := fmt.Sprintf("%d", absVal)

			if useDDC {
				args := []string{"setvcp", "10", op, valStr}
				if ddcBusNum != "" {
					args = append(args, "--bus", ddcBusNum)
				}
				exec.Command("ddcutil", args...).Run()
			} else {
				// brightnessctl set 5%+
				exec.Command("brightnessctl", "set", valStr+"%"+op).Run()
			}
		}
	}
}

func setupSignalHandler(dev *evdev.InputDevice) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		dev.Release()
		os.Exit(0)
	}()
}

func handleMuteLogic() {
	if currentMode != ModeAudio {
		return 
	}

	sinks, def, err := getSinks()
	if err != nil {
		return
	}

	if len(sinks) <= 1 {
		exec.Command("wpctl", "set-mute", "@DEFAULT_AUDIO_SINK@", "toggle").Run()
		showNotification("Mute Toggled", "")
	} else {
		switchDevice(sinks, def)
	}
}

func changeVolume(amt string) {
	val := strings.TrimLeft(amt, "+-")
	op := "+"
	if strings.HasPrefix(amt, "-") {
		op = "-"
	}
	// Add --limit 1.0 to prevent volume exceeding 100%
	exec.Command("wpctl", "set-volume", "--limit", "1.0", "@DEFAULT_AUDIO_SINK@", val+op).Run()
}

func switchDevice(sinks []string, cur string) {
	idx := 0
	for i, name := range sinks {
		if name == cur {
			idx = (i + 1) % len(sinks)
			break
		}
	}
	next := sinks[idx]
	exec.Command("pactl", "set-default-sink", next).Run()
	exec.Command("pactl", "set-sink-mute", next, "0").Run()
	showNotification("Output Switched", next)
}

func getSinks() ([]string, string, error) {
	defOut, err := exec.Command("pactl", "get-default-sink").Output()
	if err != nil {
		return nil, "", err
	}
	def := strings.TrimSpace(string(defOut))

	out, err := exec.Command("pactl", "list", "short", "sinks").Output()
	if err != nil {
		return nil, "", err
	}

	var sinks []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	excludes := []string{"hdmi", "digital-video", "spdif"}

	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		name := f[1]

		if !cfg.IncludeHDMI {
			excl := false
			lname := strings.ToLower(name)
			for _, k := range excludes {
				if strings.Contains(lname, k) {
					excl = true
					break
				}
			}
			if excl {
				continue
			}
		}
		sinks = append(sinks, name)
	}
	return sinks, def, nil
}

func findDevicePath(kw string) (string, error) {
	devs, err := evdev.ListInputDevices()
	if err != nil {
		return "", err
	}
	kw = strings.ToLower(kw)
	for _, d := range devs {
		if strings.Contains(strings.ToLower(d.Name), kw) {
			return d.Fn, nil
		}
	}
	return "", fmt.Errorf("not found: %s", kw)
}

func showNotification(title, body string) {
	exec.Command("notify-send", "-t", "1000", "-h", "string:x-canonical-private-synchronous:vol-knob", title, body).Start()
}
