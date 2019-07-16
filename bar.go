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

	"barista.run"
	"barista.run/bar"
	"barista.run/base/click"
	"barista.run/colors"
	"barista.run/format"
	"barista.run/modules/battery"
	"barista.run/modules/clock"
	"barista.run/modules/diskspace"
	"barista.run/modules/funcs"
	"barista.run/modules/media"
	"barista.run/modules/meminfo"
	"barista.run/modules/netinfo"
	"barista.run/modules/shell"
	"barista.run/modules/static"
	"barista.run/modules/volume"
	"barista.run/modules/wlan"
	"barista.run/outputs"
	"barista.run/pango"
)

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
var startScreenshot = getScreenshotTool()

func getScreenshotTool() func(bar.Event) {
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}
	return click.RunLeft(path.Join(usr.HomeDir, ".start/screenshot"))
}

func home(path string) string {
	usr, err := user.Current()
	if err != nil {
		panic(err)
	}
	return filepath.Join(usr.HomeDir, path)
}

func main() {
	/*
		TODO: enable materials
		material.Load(home("Github/material-design-icons"))
		mdi.Load(home("Github/MaterialDesign-Webfont"))
		typicons.Load(home("Github/typicons.font"))
		ionicons.LoadMd(home("Github/ionicons"))
		fontawesome.Load(home("Github/Font-Awesome"))

		colors.LoadBarConfig()
		bg := colors.Scheme("background")
		fg := colors.Scheme("statusline")
		if fg != nil && bg != nil {
			iconColor := fg.Colorful().BlendHcl(bg.Colorful(), 0.5).Clamped()
			colors.Set("dim-icon", iconColor)
			_, _, v := fg.Colorful().Hsv()
			if v < 0.3 {
				v = 0.3
			}
			colors.Set("bad", colorful.Hcl(40, 1.0, v).Clamped())
			colors.Set("degraded", colorful.Hcl(90, 1.0, v).Clamped())
			colors.Set("good", colorful.Hcl(120, 1.0, v).Clamped())
		}
	*/

	disk := diskspace.New("/").Output(func(i diskspace.Info) bar.Output {
		return outputs.Textf("%s", format.IBytesize(i.Available))
	})

	screenshot := static.New(outputs.Text("SS").
		OnClick(startScreenshot))

	vol := volume.DefaultSink().Output(func(v volume.Volume) bar.Output {
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

	refreshBrightness := make(chan struct{})
	brightness := shell.New("light", "-G").Every(500 * time.Millisecond).
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
						refreshBrightness <- struct{}{}
					case bar.ScrollDown:
						exec.Command("light", "-U", "1").Run()
						refreshBrightness <- struct{}{}
					case bar.ButtonLeft:
						if e.X > e.Width/2 {
							exec.Command("light", "-A", "5").Run()
							refreshBrightness <- struct{}{}
						} else {
							exec.Command("light", "-U", "5").Run()
							refreshBrightness <- struct{}{}
						}
					}
				})
		})
	go func() {
		for range refreshBrightness {
			brightness.Refresh()
		}
	}()

	net := netinfo.Prefix("en").Output(func(s netinfo.State) bar.Output {
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
	})

	wifi := wlan.Any().Output(func(i wlan.Info) bar.Output {
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
	})

	bat := battery.All().Output(func(i battery.Info) bar.Output {
		charging := ""
		if i.PluggedIn() {
			charging = "⚡"
		} else if !i.Discharging() {
			charging = "+"
		}
		i.SignedPower()
		return outputs.Textf("%s%.2fW %d%% (%d:%02d)",
			charging, i.Power, i.RemainingPct(), int(i.RemainingTime().Hours()),
			int(i.RemainingTime().Minutes())%60)
	})

	var lastload uint64
	var lasttotal uint64
	cpu := funcs.Every(time.Second, func(s bar.Sink) {
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
	})
	mem := meminfo.New().Output(func(i meminfo.Info) bar.Output {
		return outputs.Textf("%s",
			format.IBytesize(i["MemTotal"]-i["MemAvailable"]),
		).OnClick(startTaskManager)
	})

	localtime := clock.Local().
		Output(time.Second, func(now time.Time) bar.Output {
			return outputs.Pango(
				pango.Icon("material-today").Color(colors.Scheme("dim-icon")),
				now.Format("02.01.2006 "),
				pango.Icon("material-access-time").Color(colors.Scheme("dim-icon")),
				//now.Format("15:04:05"),
				now.Format("15:04"),
			).OnClick(click.RunLeft("gsimplecal"))
		})

	panic(barista.Run(
		disk,
		screenshot,
		vol,
		brightness,
		net,
		wifi,
		bat,
		cpu,
		mem,
		localtime,
	))
}
