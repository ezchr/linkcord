package gui

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
	"gioui.org/widget"
)

// fontChoice is a font the interface can be written in: its name and the
// files of Windows' fonts with its regular, bold and italic faces.
type fontChoice struct {
	name  string
	files []string
}

// fontChoices are the fonts offered, those that come with Windows; the
// first is Linkcord's own.
var fontChoices = []fontChoice{
	{name: ""},
	{"Segoe UI", []string{"segoeui.ttf", "segoeuib.ttf", "segoeuii.ttf", "segoeuiz.ttf"}},
	{"Arial", []string{"arial.ttf", "arialbd.ttf", "ariali.ttf", "arialbi.ttf"}},
	{"Calibri", []string{"calibri.ttf", "calibrib.ttf", "calibrii.ttf", "calibriz.ttf"}},
	{"Verdana", []string{"verdana.ttf", "verdanab.ttf", "verdanai.ttf", "verdanaz.ttf"}},
	{"Tahoma", []string{"tahoma.ttf", "tahomabd.ttf"}},
	{"Trebuchet MS", []string{"trebuc.ttf", "trebucbd.ttf", "trebucit.ttf", "trebucbi.ttf"}},
	{"Georgia", []string{"georgia.ttf", "georgiab.ttf", "georgiai.ttf", "georgiaz.ttf"}},
	{"Times New Roman", []string{"times.ttf", "timesbd.ttf", "timesi.ttf", "timesbi.ttf"}},
	{"Comic Sans MS", []string{"comic.ttf", "comicbd.ttf", "comici.ttf", "comicz.ttf"}},
	{"Consolas", []string{"consola.ttf", "consolab.ttf", "consolai.ttf", "consolaz.ttf"}},
}

func windowsFonts() string { return filepath.Join(os.Getenv("WINDIR"), "Fonts") }

// installedFonts are the choices this Windows has the regular face of.
func installedFonts() []fontChoice {
	var out []fontChoice
	for _, f := range fontChoices {
		if len(f.files) == 0 {
			out = append(out, f)
			continue
		}
		if _, err := os.Stat(filepath.Join(windowsFonts(), f.files[0])); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// fontLabel is how a choice is called in the settings.
func fontLabel(name string) string {
	if name == "" {
		return "Linkcord Default"
	}
	return name
}

// newShaper shapes text in the font named, Linkcord's own when none or when
// it is not there, falling back to the bundled and selected fonts for what
// it lacks. The faces loaded first are the default.
func newShaper(name string) *text.Shaper {
	needs := fontNeeds.Load()
	defer fontsLoaded.Store(needs)
	var collection []font.FontFace
	dir := windowsFonts()
	var chosen []string
	if i := slices.IndexFunc(fontChoices, func(f fontChoice) bool { return f.name == name }); i > 0 {
		chosen = fontChoices[i].files
	}
	load := func(file string) {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return
		}
		faces, err := opentype.ParseCollection(data)
		if err != nil {
			slog.Warn("failed to parse font", "font", file, "err", err)
			return
		}
		collection = append(collection, faces...)
	}
	// The first of the faces in a big font file is enough: the others are
	// variants, such as a UI version, of the same letters.
	loadFirst := func(files []string) {
		for _, file := range files {
			data, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil {
				continue
			}
			face, err := opentype.ParseFirst(data)
			if err != nil {
				continue
			}
			collection = append(collection, face)
			return
		}
	}
	for _, file := range chosen {
		load(file)
	}
	collection = append(collection, gofont.Collection()...)
	if faces, err := opentype.ParseCollection(narrowSpace(colorEmoji)); err == nil {
		collection = append(collection, faces...)
	} else {
		slog.Warn("failed to parse the emoji font", "err", err)
	}
	for _, file := range fallbackFonts {
		if !slices.Contains(chosen, file) {
			load(file)
		}
	}
	if needs&needSymbols != 0 {
		for _, file := range symbolFonts {
			if !slices.Contains(chosen, file) {
				load(file)
			}
		}
	}
	if needs&needCJK != 0 {
		loadFirst(cjkFonts)
	}
	if needs&needKorean != 0 {
		loadFirst(koreanFonts)
	}
	for need, files := range scriptFonts {
		if needs&need != 0 {
			loadFirst(files)
		}
	}
	// Not the rest of Windows' fonts: Gio would read whole any font that
	// has a character missing here, a hundred megabytes for one mark.
	return text.NewShaper(text.NoSystemFonts(), text.WithCollection(collection))
}

// fontView is the choice of font in the settings.
type fontView struct {
	btns map[string]*widget.Clickable
	// loading is the font being loaded, which takes a moment.
	loading string
}

func (f *fontView) btn(name string) *widget.Clickable {
	if f.btns == nil {
		f.btns = make(map[string]*widget.Clickable)
	}
	b, ok := f.btns[name]
	if !ok {
		b = new(widget.Clickable)
		f.btns[name] = b
	}
	return b
}

// setFont writes the interface in the font named from now on.
func (c *chatView) setFont(name string) {
	u := c.u
	f := &c.settings.general.font
	if name == u.app.Font || f.loading != "" {
		return
	}
	u.app.Font = name
	u.saveAppSettings()
	f.loading = name
	go func() {
		sh := newShaper(name)
		u.post(func() {
			f.loading = ""
			// What was shaped with the old one is shaped again, as text
			// widgets notice the shaper change.
			u.th.Shaper = sh
			u.win.Invalidate()
		})
	}()
}
