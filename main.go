// visextract unpacks MindVision Installer VISE (Windows) archives without running the installer.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/emdzej/visextract/internal/source"
	"github.com/emdzej/visextract/internal/vise"
)

// version is set at build time by the release workflow (-ldflags "-X main.version=<tag>").
var version = "0.1.0"

const usage = `visextract %s - unpack MindVision Installer VISE (Windows) archives

usage:
  visextract list    [-setup NAME] <source>
  visextract extract [-setup NAME] [-f] <source> <outdir> [path...]
  visextract summary [-setup NAME] <source>
  visextract version

<source> is a disc image (.iso, raw .bin), a folder with the installer's files, or the setup program
itself (its segment files, <name>.002 and on, must be next to it). Without -setup the setup program is
found among the .exe files at the top of the source.

extract writes the install folder's contents to <outdir> (all files, or only the given paths and
folders), checks every file against the archive's CRC-32 and keeps the archive's modification times.
Existing files are not overwritten unless -f is given.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "list":
		err = runList(args)
	case "extract", "x":
		err = runExtract(args)
	case "summary":
		err = runSummary(args)
	case "version", "-version", "--version":
		fmt.Println(version)
	case "help", "-h", "-help", "--help":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, "visextract: unknown command %q\n\n"+usage, cmd, version)
		os.Exit(2)
	}
	if err != nil {
		var u usageError
		if errors.As(err, &u) {
			fmt.Fprintf(os.Stderr, "visextract: %v\n\n"+usage, err, version)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "visextract: %v\n", err)
		os.Exit(1)
	}
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

// open resolves <source> (and -setup) to an archive.
func open(src, setup string) (*vise.Archive, source.Source, error) {
	st, err := os.Stat(src)
	if err != nil {
		return nil, nil, err
	}
	// The setup program itself: open its folder.
	if !st.IsDir() && strings.EqualFold(filepath.Ext(src), ".exe") {
		if setup == "" {
			setup = filepath.Base(src)
		}
		src = filepath.Dir(src)
	}
	s, err := source.Open(src)
	if err != nil {
		return nil, nil, err
	}
	var a *vise.Archive
	if setup == "" {
		a, err = vise.OpenAny(s)
	} else {
		a, err = vise.Open(s, setup)
	}
	if err != nil {
		s.Close()
		return nil, nil, err
	}
	return a, s, nil
}

func parse(name string, args []string, npos int, more bool, extra func(*flag.FlagSet)) (*flag.FlagSet, string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	setup := fs.String("setup", "", "name of the setup program in the source")
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(args); err != nil {
		return nil, "", usageError{err.Error()}
	}
	if fs.NArg() < npos || (!more && fs.NArg() > npos) {
		return nil, "", usageError{fmt.Sprintf("%s: wrong number of arguments", name)}
	}
	return fs, *setup, nil
}

func runList(args []string) error {
	fs, setup, err := parse("list", args, 1, false, nil)
	if err != nil {
		return err
	}
	a, s, err := open(fs.Arg(0), setup)
	if err != nil {
		return err
	}
	defer s.Close()
	for _, e := range a.Entries {
		if e.Dir {
			fmt.Printf("%10s  %10s  %8s  %16s  %s/\n", "", "", "", "", e.Path)
			continue
		}
		fmt.Printf("%10d  %10d  %08x  %s  %s\n", e.Size, e.CSize, e.CRC, e.ModTime.Format("2006-01-02 15:04"), e.Path)
	}
	return nil
}

func runSummary(args []string) error {
	fs, setup, err := parse("summary", args, 1, false, nil)
	if err != nil {
		return err
	}
	a, s, err := open(fs.Arg(0), setup)
	if err != nil {
		return err
	}
	defer s.Close()
	fmt.Println(a.Summary())
	return nil
}

func selected(p string, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		w = strings.Trim(filepath.ToSlash(w), "/")
		if strings.EqualFold(p, w) || strings.HasPrefix(strings.ToLower(p), strings.ToLower(w)+"/") {
			return true
		}
	}
	return false
}

func runExtract(args []string) error {
	var force *bool
	fs, setup, err := parse("extract", args, 2, true, func(fs *flag.FlagSet) {
		force = fs.Bool("f", false, "overwrite existing files")
	})
	if err != nil {
		return err
	}
	a, s, err := open(fs.Arg(0), setup)
	if err != nil {
		return err
	}
	defer s.Close()
	out, want := fs.Arg(1), fs.Args()[2:]
	files, total := 0, uint64(0)
	for i := range a.Entries {
		e := &a.Entries[i]
		if !selected(e.Path, want) {
			continue
		}
		dst := filepath.Join(out, filepath.FromSlash(e.Path))
		if e.Dir {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Stat(dst); err == nil && !*force {
			return fmt.Errorf("%s exists (use -f to overwrite)", dst)
		}
		data, err := a.Extract(e)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		// Write <name>.part and rename it when complete: an interrupted run leaves no truncated file.
		part := dst + ".part"
		if err := os.WriteFile(part, data, 0o644); err != nil {
			os.Remove(part)
			return err
		}
		if err := os.Rename(part, dst); err != nil {
			os.Remove(part)
			return err
		}
		if !e.ModTime.IsZero() {
			os.Chtimes(dst, e.ModTime, e.ModTime)
		}
		fmt.Printf("%10d  %s\n", e.Size, e.Path)
		files++
		total += uint64(e.Size)
	}
	if files == 0 && len(want) > 0 {
		return fmt.Errorf("nothing matched %s", strings.Join(want, ", "))
	}
	fmt.Printf("%d files, %d bytes in %s\n", files, total, path.Clean(filepath.ToSlash(out)))
	return nil
}
