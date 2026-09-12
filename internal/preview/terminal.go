package preview

import (
	"fmt"
	"io"
)

func Terminal(w io.Writer, v View, all, color bool) error {
	if _, err := fmt.Fprintf(w, "Hue scene preview — %s\n", v.Source); err != nil {
		return err
	}
	count := 0
	for _, s := range v.Scenes {
		if !all && !s.Changed {
			continue
		}
		count++
		if _, err := fmt.Fprintf(w, "\n%s · %s [%s]\n  %s\n", s.Group, s.Name, s.Change, s.Address); err != nil {
			return err
		}
		if s.Notice != "" {
			fmt.Fprintln(w, "  "+s.Notice)
		}
		rows := 0
		for _, r := range s.Rows {
			if !all && !r.Changed {
				continue
			}
			rows++
			name := r.Name
			if name == "" {
				name = r.ID
			}
			if _, err := fmt.Fprintf(w, "  %s\n    %s → %s\n", name, terminalSample(r.Before, color), terminalSample(r.After, color)); err != nil {
				return err
			}
		}
		if rows == 0 && s.Notice == "" {
			fmt.Fprintln(w, "  No lighting action changes (scene metadata or other settings changed).")
		}
	}
	if count == 0 {
		_, err := fmt.Fprintln(w, "No scene changes.")
		return err
	}
	_, err := fmt.Fprintln(w, "\nSwatches show normalized color; brightness is listed separately. Screen colors are approximate.")
	return err
}
func terminalSample(s Sample, color bool) string {
	if color && s.HasColor {
		return fmt.Sprintf("\x1b[48;2;%d;%d;%dm  \x1b[0m %s", s.R, s.G, s.B, s.Text)
	}
	return s.Text
}
