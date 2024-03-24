package scanner

import (
	"io/fs"
	"path/filepath"
	"strings"
)

type Entry struct {
	Author   string
	Title    string
	Filename string
	Path     string
}

type Battle struct {
	Name    string
	Entries []Entry
}

func GetAllBattles(
	getNamesFunc func() ([]string, error),
	getBattleFunc func(name string) (Battle, error),
) ([]Battle, error) {
	names, err := getNamesFunc()
	if err != nil {
		return nil, err
	}
	var battles []Battle
	for _, name := range names {
		battle, err := getBattleFunc(name)
		if err != nil {
			return nil, err
		}
		battles = append(battles, battle)
	}
	return battles, nil
}

// FSScanner .
type FSScanner struct {
	Fsys fs.FS
}

func (s *FSScanner) GetBattleNames() ([]string, error) {
	var res []string
	des, err := fs.ReadDir(s.Fsys, ".")
	if err != nil {
		return nil, err
	}
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		res = append(res, de.Name())
	}
	return res, nil
}

func (s *FSScanner) GetBattle(name string) (Battle, error) {
	battle := Battle{
		Name: name,
	}

	entries, err := fs.ReadDir(s.Fsys, name)
	if err != nil {
		return battle, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		ext := filepath.Ext(filename)
		switch strings.ToLower(ext) {
		case ".wav", ".mp3", ".ogg", ".flac":
			// do nothing
		default:
			continue
		}

		author, title, ok := strings.Cut(strings.TrimSuffix(filename, ext), "-")
		if !ok {
			author, title = title, author
		}

		author = strings.TrimSpace(replaceSpaces.Replace(author))
		title = strings.TrimSpace(replaceSpaces.Replace(title))

		fullPath := filepath.Join(name, filename)
		battle.Entries = append(battle.Entries, Entry{
			Author:   author,
			Title:    title,
			Filename: filename,
			Path:     fullPath,
		})
	}
	return battle, nil
}

var replaceSpaces = strings.NewReplacer(generateReplacerPairs("-_", " ")...)

func generateReplacerPairs(s, replacement string) []string {
	pairs := make([]string, 2*len(s))
	for i, char := range []rune(s) {
		pairs[i*2] = string(char)
		pairs[i*2+1] = replacement
	}
	return pairs
}
