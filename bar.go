package main

import (
	"fmt"
	"image/color"
	"log"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"time"

	"github.com/c9s/goprocinfo/linux"
	"github.com/fsnotify/fsnotify"
	"github.com/godbus/dbus/v5"
	"github.com/martinlindhe/unit"

	"barista.run"
	"barista.run/bar"
	"barista.run/base/click"
	"barista.run/colors"
	"barista.run/format"
	"barista.run/modules/battery"
	"barista.run/modules/clock"
	"barista.run/modules/cputemp"
	"barista.run/modules/diskspace"
	"barista.run/modules/funcs"
	"barista.run/modules/media"
	"barista.run/modules/meminfo"
	"barista.run/modules/netinfo"
	"barista.run/modules/shell"
	"barista.run/modules/static"
	"barista.run/modules/volume"
	"barista.run/modules/volume/pulseaudio"
	"barista.run/modules/wlan"
	"barista.run/outputs"
	"barista.run/pango"
)

var bus = func() *dbus.Conn {
	b, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Println(err)
		return nil
	}

	return b
}()

var spacer = pango.Text(" ").XXSmall()

func truncate(in string, l int) string {
	if len([]rune(in)) <= l {
		return in
	}
	return string([]rune(in)[:l-1]) + "⋯"
}

func hms(d time.Duration) (h int, m int, s int) {
	h = int(d.Hours())
	m = int(d.Minutes()) % 60
	s = int(d.Seconds()) % 60
	return
}

func formatMediaTime(d time.Duration) string {
	h, m, s := hms(d)
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func mediaFormatFunc(m media.Info) bar.Output {
	if m.PlaybackStatus == media.Stopped || m.PlaybackStatus == media.Disconnected {
		return nil
	}
	artist := truncate(m.Artist, 20)
	title := truncate(m.Title, 40-len(artist))
	if len(title) < 20 {
		artist = truncate(m.Artist, 40-len(title))
	}
	iconAndPosition := pango.Icon("fa-music").Color(colors.Hex("#f70"))
	if m.PlaybackStatus == media.Playing {
		iconAndPosition.Append(
			spacer, pango.Textf("%s/%s",
				formatMediaTime(m.Position()),
				formatMediaTime(m.Length)),
		)
	}
	return outputs.Pango(iconAndPosition, spacer, title, " - ", artist)
}

var startTaskManager = click.RunLeft("i3-sensible-terminal", "-e", "htop")

func runTool(executable string) func(bar.Event) {
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}
	return click.RunLeft(path.Join(usr.HomeDir, ".start", executable))
}

func home(path string) string {
	usr, err := user.Current()
	if err != nil {
		panic(err)
	}
	return filepath.Join(usr.HomeDir, path)
}

type mybar struct {
	mods []bar.Module
}

func (b *mybar) add(mod bar.Module) {
	b.mods = append(b.mods, mod)
}

func (b *mybar) disk() {
	b.add(diskspace.New("/").Output(func(i diskspace.Info) bar.Output {
		return outputs.Textf("%s", format.IBytesize(i.Available))
	}))
}

func (b *mybar) screenshot() {
	b.add(static.New(outputs.Text("SS").
		OnClick(runTool("screenshot"))))
}

func (b *mybar) keyboard() {
	b.add(static.New(outputs.Text("⌨️").
		OnClick(click.Left(func() {
			obj := bus.Object("sm.puri.OSK0", "/sm/puri/OSK0")

			prop, err := obj.GetProperty("sm.puri.OSK0.Visible")
			if err != nil {
				log.Println(err)
				return
			}

			b, ok := prop.Value().(bool)
			if !ok {
				log.Println("Value not a bool")
				return
			}

			obj.Call("sm.puri.OSK0.SetVisible", 0, !b)
		}))))
}

func (b *mybar) vol() {
	volume.New(pulseaudio.DefaultSink()).Output(func(v volume.Volume) bar.Output {
		if v.Mute {
			return outputs.
				Pango("♪: ", pango.Icon("ion-volume-off"), "-").
				Color(colors.Scheme("degraded"))
		}
		iconName := "mute"
		pct := v.Pct()
		if pct > 66 {
			iconName = "high"
		} else if pct > 33 {
			iconName = "low"
		}
		return outputs.Pango(
			pango.Icon("ion-volume-"+iconName),
			spacer,
			pango.Textf("♪: %2d%%", pct),
		).OnClick(func(e bar.Event) {
			switch e.Button {
			case bar.ScrollUp:
				v.SetVolume(v.Vol + (v.Max-v.Min)/100)
			case bar.ScrollDown:
				v.SetVolume(v.Vol - (v.Max-v.Min)/100)
			case bar.ButtonLeft:
				if v.Mute {
					v.SetMuted(false)
				} else if e.X < e.Width/3 {
					v.SetVolume(v.Vol - (v.Max-v.Min)/20)
				} else if e.X < e.Width*2/3 {
					v.SetMuted(true)
				} else {
					v.SetVolume(v.Vol + (v.Max-v.Min)/20)
				}
			}
		})
	})
}

func (b *mybar) brightness() {
	brightness := shell.New("light", "-G").
		Output(func(value string) bar.Output {
			i, err := strconv.ParseFloat(value, 64)
			if err != nil {
				outputs.Text("☀: ??")
			}
			return outputs.Textf("☀: %.0f%%", i).
				OnClick(func(e bar.Event) {
					switch e.Button {
					case bar.ScrollUp:
						exec.Command("light", "-A", "1").Run()
					case bar.ScrollDown:
						exec.Command("light", "-U", "1").Run()
					case bar.ButtonLeft:
						if e.X > e.Width/2 {
							exec.Command("light", "-A", "5").Run()
						} else {
							exec.Command("light", "-U", "5").Run()
						}
					}
				})
		})
	go func() {
		// Fall back to polling if there is an error
		defer brightness.Every(500 * time.Millisecond)

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return
		}
		err = watcher.Add("/sys/class/backlight/intel_backlight/brightness")
		if err != nil {
			return
		}

		for ev := range watcher.Events {
			if ev.Op == fsnotify.Write {
				brightness.Refresh()
			}
		}
	}()
	b.add(brightness)
}

func (b *mybar) net() {
	b.add(netinfo.Prefix("en").Output(func(s netinfo.State) bar.Output {
		if len(s.IPs) > 0 {
			return outputs.Textf("E: %s", s.IPs[0]).Color(color.RGBA{
				A: 255, G: 255,
			})
		}
		if s.Connected() {
			return outputs.Text("E: no ip").Color(color.RGBA{
				R: 255, A: 255,
			})
		}
		if s.Connecting() {
			return outputs.Text("E: ...").Color(color.RGBA{
				R: 255, G: 255, A: 255,
			})
		}
		return nil
	}))
}

func (b *mybar) wifi() {
	b.add(wlan.Any().Output(func(i wlan.Info) bar.Output {
		freq := "2.4GHz"
		if i.Frequency.Gigahertz() > 4 {
			freq = "5GHz"
		}
		switch {
		case !i.Enabled():
			return nil
		case i.Connecting():
			return outputs.Text("W: ???")
		case !i.Connected():
			return outputs.Text("W: down")
		case len(i.IPs) < 1:
			return outputs.Textf("W: %s (%s) ???", i.SSID, freq).
				Color(color.RGBA{
					A: 255, G: 255,
				})
		default:
			return outputs.Textf("W: %s (%s) %s", i.SSID, freq, i.IPs[0]).
				Color(color.RGBA{
					A: 255, G: 255,
				})
		}
	}))
}

func (b *mybar) bat() {
	batLastRemaining := 1.0
	b.add(battery.All().Output(func(i battery.Info) bar.Output {
		charging := ""
		if i.PluggedIn() {
			charging = "⚡"
		} else if !i.Discharging() {
			charging = "+"
		}
		// Notify user about low battery
		if i.Remaining() <= .15 && batLastRemaining > .15 {
			exec.Command("dunstify", "Battery low (15% remaining)").Run()
		}
		if i.Remaining() <= .05 && batLastRemaining > .05 {
			exec.Command("dunstify", "Battery low (5% remaining)", "-t", "0").Run()
		}
		batLastRemaining = i.Remaining()
		return outputs.Textf("%s%.2fW %d%% (%d:%02d)",
			charging, i.Power, i.RemainingPct(), int(i.RemainingTime().Hours()),
			int(i.RemainingTime().Minutes())%60)
	}))
}

func (b *mybar) cpu() {
	var lastload uint64
	var lasttotal uint64
	b.add(funcs.Every(time.Second, func(s bar.Sink) {
		stat, err := linux.ReadStat("/proc/stat")
		if err != nil {
			s.Output(outputs.Text("").Error(err))
		}

		load := stat.CPUStatAll.User + stat.CPUStatAll.System + stat.CPUStatAll.Steal +
			stat.CPUStatAll.Nice
		l := load - lastload
		lastload = load
		total := load + stat.CPUStatAll.Idle
		t := total - lasttotal
		lasttotal = total
		s.Output(outputs.Textf("%.1f%%",
			float64(l)/float64(t)*100,
		).OnClick(startTaskManager))
	}))
}

func (b *mybar) temp() {
	b.add(cputemp.New().Output(func(t unit.Temperature) bar.Output {
		return outputs.Textf("%.0f°C", t.Celsius())
	}))
}

func (b *mybar) mem() {
	b.add(meminfo.New().Output(func(i meminfo.Info) bar.Output {
		return outputs.Textf("%s",
			format.IBytesize(i["MemTotal"]-i["MemAvailable"]),
		).OnClick(startTaskManager)
	}))
}

func (b *mybar) localtime() {
	b.add(clock.Local().
		Output(time.Second, func(now time.Time) bar.Output {
			return outputs.Pango(
				now.Format("02.01.2006 "),
				now.Format("15:04"),
			).OnClick(click.RunLeft("gsimplecal"))
		}))
}

func main() {
	b := &mybar{
		mods: make([]bar.Module, 0),
	}

	b.disk()
	b.screenshot()
	b.keyboard()
	b.vol()
	b.brightness()
	b.net()
	b.wifi()
	b.bat()
	b.cpu()
	b.temp()
	b.mem()
	b.localtime()

	panic(barista.Run(b.mods...))
}
