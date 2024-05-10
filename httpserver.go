package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/some-programs/battlr/assets"
	"github.com/some-programs/battlr/pkg/db"
	"github.com/some-programs/battlr/pkg/scanner"
	"golang.org/x/sync/errgroup"
)

type errorInfo struct {
	Error string `json:"err"`
	Type  string `json:"type"`
}

func inspectError(err error) []errorInfo {
	var info []errorInfo
	e := err
	for e != nil {
		t := reflect.TypeOf(e)
		info = append(info, errorInfo{
			Error: e.Error(),
			Type:  t.String(),
		})
		e = errors.Unwrap(e)
	}
	return info
}

type AppHandler func(http.ResponseWriter, *http.Request) error

func (fn AppHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := fn(w, r); err != nil {
		errInfo := inspectError(err)
		data, err := json.Marshal(&errInfo)
		w.WriteHeader(500)
		if err != nil {
			slog.Error("marshalling inspect error", "error", err)
			w.Write([]byte("[]"))
		} else {
			// logger.Err(err).RawJSON("errors", data).Msg("error info")
			slog.Error("error info", "json", data)
			w.Write(data)
		}
	}
}

// ServerConfig .
type ServerConfig struct {
	Unrestricted     bool
	ShowScores       bool
	FullResultsOrder bool
}

type Server struct {
	ServerConfig
	DB          *db.DB
	BattlesFsys fs.FS
}

func (server *Server) RegisterHandlers(h *http.ServeMux, apiKey string, battlesFsys fs.FS) {
	authMiddleware := BearerAuthMiddleware(apiKey)
	h.Handle("GET /battles/", server.Index())
	h.Handle("GET /battles/vote/{name}/", ClientIDMiddleware()(server.VoteForm()))
	h.Handle("GET /zip/{name}/", server.Zip())
	h.Handle("GET /battles/results/{name}/", ClientIDMiddleware()(server.Results()))
	h.Handle("GET /events/{name}/", server.battleEvents())
	h.Handle("GET /static/", http.FileServerFS(assets.StaticHashFS))

	h.Handle("/api/vote/", ClientIDMiddleware()(server.Vote()))
	h.Handle("/api/unvote/", ClientIDMiddleware()(server.UnVote()))

	h.Handle("/api/battles/{name}/", authMiddleware(server.GetBattleData()))
	h.Handle("/api/scan/", authMiddleware(server.Scan()))
	h.Handle("/api/open/{name}/", authMiddleware(server.OpenBattle()))
	h.Handle("/api/close/{name}/", authMiddleware(server.CloseBattle()))
	h.Handle("/api/hide/{name}/", authMiddleware(server.HideBattle()))
	h.Handle("/api/unhide/{name}/", authMiddleware(server.UnhideBattle()))
	h.Handle("/dl/", http.StripPrefix("/dl/", server.ResolveFilename(http.FileServerFS(battlesFsys))))

	h.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`User-agent: *
Disallow: /`))
	})

}

func (s *Server) ResolveFilename(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		battleName, entryID, _ := strings.Cut(r.URL.Path, "/")
		battle, err := s.DB.GetBattle(battleName)
		if err != nil {
			slog.Error("error", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if battle == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		entry, ok := battle.GetEntryByID(entryID)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		p := strings.Replace(r.URL.Path, entryID, entry.Filename, 1)
		rp := strings.Replace(r.URL.RawPath, entryID, entry.Filename, 1)

		r2 := new(http.Request)
		*r2 = *r
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = p
		r2.URL.RawPath = rp
		h.ServeHTTP(w, r2)
	})
}

func (s *Server) Index() AppHandler {
	tmpl, err := template.New("base.html").
		Funcs(template.FuncMap{
			"static": assets.StaticHashFS.HashName,
		},
		).
		ParseFS(assets.TemplateFS, "template/base.html", "template/index.html")
	if err != nil {
		slog.Error("failed to parse clients template",
			"err", err,
		)
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		allBattles, err := s.DB.GetAllBattles()
		if err != nil {
			slog.Error("err", "err", err)
			return err
		}

		var battles []db.Battle
		for _, b := range allBattles {
			if b.Hidden {
				continue
			}
			battles = append(battles, b)
		}

		templateData := struct {
			Title   string
			Battles []db.Battle
		}{
			Title:   "Battles",
			Battles: battles,
		}

		w.WriteHeader(http.StatusOK)

		if err := tmpl.Execute(w, &templateData); err != nil {
			slog.Info("error", "err", err)
			return err
		}
		return nil
	}
}

func (s *Server) Results() AppHandler {
	tmpl, err := template.New("base.html").
		Funcs(template.FuncMap{
			"static": assets.StaticHashFS.HashName,
			"add": func(i, j int) int {
				return i + j
			},
		},
		).
		ParseFS(assets.TemplateFS, "template/base.html", "template/battle-results.html")
	if err != nil {
		slog.Error("failed to parse clients template",
			"err", err,
		)
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		name := r.PathValue("name")
		battle, err := s.DB.GetBattle(name)
		if err != nil {
			return err
		}
		if battle == nil {
			return db.NotFound
		}

		if !s.Unrestricted {
			if battle.Hidden {
				return db.NotFound
			}
			if battle.ClosedAt.IsZero() {
				return s.ErrorPage(r.Context(), w, r, "cannot view results while voting is open", "/battles/")
			}
		}

		allVotes, err := s.DB.GetAllVotes(name)
		if err != nil {
			return err
		}
		if len(allVotes) == 0 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`No votes recorded`))
			return nil
		}
		sumScores := db.SumScores(allVotes)
		numVoters := len(allVotes)

		topPlaces := battle.Entries.Places(sumScores)
		if len(topPlaces) > 3 {
			topPlaces = topPlaces[:3]
		}

		if s.FullResultsOrder {
			battle.Entries.SortByScore(sumScores)
		} else {
			battle.Entries.Shuffle()
		}
		rest := topPlaces.Diff(battle.Entries)
		rest.SortByName()
		templateData := struct {
			Title     string
			Battle    db.Battle
			NumVoters int
			SumScores db.ScoreMap
			Config    ServerConfig
			TopPlaces []db.Entries
			Rest      db.Entries
		}{
			Title:     "Results",
			Battle:    *battle,
			NumVoters: numVoters,
			SumScores: sumScores,
			Config:    s.ServerConfig,
			TopPlaces: topPlaces,
			Rest:      rest,
		}

		w.WriteHeader(http.StatusOK)

		if err := tmpl.Execute(w, &templateData); err != nil {
			slog.Info("error", "err", err)
			return err
		}

		return nil
	}

}

func (s *Server) VoteForm() AppHandler {

	tmpl, err := template.New("base.html").
		Funcs(template.FuncMap{
			"static": assets.StaticHashFS.HashName,
			"add": func(i, j int) int {
				return i + j
			},
			"voteclass": func(scores db.ScoreMap, entryID string, score int) string {
				if scores == nil {
					return ""
				}
				v, ok := scores[entryID]
				if !ok {
					return ""
				}
				if v == 0 {
					return ""
				}
				if v == score {
					return "vote-yes"
				}
				return ""
			},
		},
		).
		ParseFS(assets.TemplateFS, "template/base.html", "template/battle-vote.html")
	if err != nil {
		slog.Error("failed to parse clients template",
			"err", err,
		)
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		name := r.PathValue("name")
		battle, err := s.DB.GetBattle(name)
		if err != nil {
			return err
		}
		if battle == nil {
			return db.NotFound
		}

		if !s.Unrestricted {
			if battle.Hidden {
				return db.NotFound
			}
			if !battle.ClosedAt.IsZero() {
				return errors.New("voting is closed")
			}
		}

		shuffleSeedStr := r.URL.Query().Get("shuffle")

		if shuffleSeedStr == "" {
			shuffleSeedStr = "default shuffle order"
		}

		{
			h := sha256.New()
			h.Write([]byte(shuffleSeedStr))
			sum := h.Sum(nil)
			var seed [32]byte
			copy(seed[:], sum[:32])
			entries := slices.Clone(battle.Entries)
			rnd := rand.New(rand.NewChaCha8(seed))
			rnd.Shuffle(len(entries), func(i,
				j int) {
				entries[i], entries[j] = entries[j], entries[i]
			})
			battle.Entries = entries

		}

		clientID := getClientID(ctx)
		if clientID == "" {
			return errors.New("no client id found")
		}

		votes, err := s.DB.GetVotes(battle.Name, clientID)
		if err != nil && err != db.NotFound {
			return err
		}

		if votes == nil {
			votes = &db.Votes{}
		}

		templateData := struct {
			Title  string
			Battle db.Battle
			Votes  db.Votes
			Config ServerConfig
		}{
			Title:  "Voting",
			Battle: *battle,
			Votes:  *votes,
			Config: s.ServerConfig,
		}

		w.WriteHeader(http.StatusOK)

		if err := tmpl.Execute(w, &templateData); err != nil {
			slog.Info("error", "err", err)
			return err
		}

		return nil
	}

}

// VoteReqest .
type VoteReqest struct {
	BattleName string `json:"battle_name"`
	EntryID    string `json:"entry_id"`
	Score      int    `json:"score"`
}

func (s *Server) Vote() AppHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		clientID := getClientID(ctx)
		if clientID == "" {
			return errors.New("no client id found")
		}

		data, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}

		var req VoteReqest
		if err := json.Unmarshal(data, &req); err != nil {
			return err
		}

		battle, err := s.DB.GetBattle(req.BattleName)
		if err != nil {
			slog.Error("error", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return err
		}
		if battle == nil {
			w.WriteHeader(http.StatusNotFound)
			return nil
		}

		_, ok := battle.GetEntryByID(req.EntryID)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return nil
		}

		if !s.Unrestricted {
			if battle.Hidden {
				return db.NotFound
			}
			if !battle.ClosedAt.IsZero() {
				return s.ErrorPage(r.Context(), w, r, "voting is closed", "/battles/")
			}
		}

		if err := s.DB.UpdateVote(req.BattleName, req.EntryID, clientID, req.Score); err != nil {
			return err
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))

		return nil
	}

}

type UnVoteReqest struct {
	BattleName string `json:"battle_name"`
}

func (s *Server) UnVote() AppHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		clientID := getClientID(ctx)
		if clientID == "" {
			return errors.New("no client id found")
		}

		data, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}

		var req VoteReqest
		if err := json.Unmarshal(data, &req); err != nil {
			return err
		}

		battle, err := s.DB.GetBattle(req.BattleName)
		if err != nil {
			slog.Error("error", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return err
		}
		if battle == nil {
			w.WriteHeader(http.StatusNotFound)
			return nil
		}
		if err := s.DB.RemoveVotes(req.BattleName, clientID); err != nil {
			return err
		}

		w.WriteHeader(http.StatusOK)

		return nil

	}
}

func (s *Server) Zip() AppHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		battleName := r.PathValue("name")
		battle, err := s.DB.GetBattle(battleName)
		if err != nil {
			if err == db.NotFound {
				w.WriteHeader(http.StatusNotFound)
				return nil
			}
			return err
		}

		if !s.Unrestricted {
			if battle.Hidden {
				return db.NotFound
			}
			if battle.IsVotingOpen() {
				w.WriteHeader(http.StatusForbidden)
				return nil
			}
		}
		subFs, err := fs.Sub(s.BattlesFsys, battleName)
		if err != nil {
			return err
		}

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		w.WriteHeader(http.StatusOK)

		zw := zip.NewWriter(w)

		defer zw.Close()

		return fs.WalkDir(subFs, ".", func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("zip: cannot add non-regular file")
			}
			h, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			h.Name = name
			h.Method = zip.Store
			fw, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			f, err := subFs.Open(name)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(fw, f)
			return err
		})
	}
}

func (s *Server) CloseBattle() AppHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		battleName := r.PathValue("name")
		_, err := s.DB.GetBattle(battleName)
		if err != nil {
			if err == db.NotFound {
				w.WriteHeader(http.StatusNotFound)
				return nil
			}
			return err
		}
		return s.DB.CloseBattle(battleName)
	}
}

func (s *Server) OpenBattle() AppHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		battleName := r.PathValue("name")
		_, err := s.DB.GetBattle(battleName)
		if err != nil {
			if err == db.NotFound {
				w.WriteHeader(http.StatusNotFound)
				return nil
			}
			return err
		}
		return s.DB.OpenBattle(battleName)
	}
}

func (s *Server) HideBattle() AppHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		battleName := r.PathValue("name")
		_, err := s.DB.GetBattle(battleName)
		if err != nil {
			if err == db.NotFound {
				w.WriteHeader(http.StatusNotFound)
				return nil
			}
			return err
		}
		return s.DB.HideBattle(battleName)
	}
}

func (s *Server) UnhideBattle() AppHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		battleName := r.PathValue("name")
		_, err := s.DB.GetBattle(battleName)
		if err != nil {
			if err == db.NotFound {
				w.WriteHeader(http.StatusNotFound)
				return nil
			}
			return err
		}
		return s.DB.UnhideBattle(battleName)
	}
}

func (s *Server) Scan() AppHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		fsc := scanner.FSScanner{Fsys: s.BattlesFsys}
		battles, err := scanner.GetAllBattles(fsc.GetBattleNames, fsc.GetBattle)
		if err != nil {
			return err
		}
		var errs []error
		for _, b := range battles {
			slog.Info("updating", "battle", b.Name)
			if err := s.DB.UpdateBattle(b); err != nil {
				slog.Error("could not update battle", "err", err)
				errs = append(errs, err)
			}

		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}
		return nil
	}
}

// BattleDataResponse .
type BattleDataResponse struct {
	Battle    db.Battle
	Votes     []db.Votes
	ScoresSum map[string]int
}

func (s *Server) GetBattleData() AppHandler {

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		name := r.PathValue("name")
		battle, err := s.DB.GetBattle(name)
		if err != nil {
			return err
		}
		if battle == nil {
			return db.NotFound
		}

		allVotes, err := s.DB.GetAllVotes(name)
		if err != nil {
			return err
		}

		sumScores := db.SumScores(allVotes)
		battle.Entries.SortByScore(sumScores)
		WriteJSONResponse(ctx, w, http.StatusOK,
			BattleDataResponse{
				Votes:     allVotes,
				Battle:    *battle,
				ScoresSum: sumScores,
			})
		return nil
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  10 * 1024,
	WriteBufferSize: 10 * 1024,
}

const (
	pongWait   = time.Minute
	pingPeriod = (pongWait * 9) / 10
	writeWait  = 10 * time.Second
)

func (s *Server) battleEvents() AppHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		battleName := r.PathValue("name")

		battle, err := s.DB.GetBattle(battleName)
		if err != nil {
			slog.Error("error", "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return err
		}
		if battle == nil {
			w.WriteHeader(http.StatusNotFound)
			return nil
		}

		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println(err)
			return err
		}
		defer ws.Close()

		ws.SetPongHandler(func(string) error {
			ws.SetReadDeadline(time.Now().Add(pongWait))
			return nil
		})
		ws.SetReadDeadline(time.Now().Add(pongWait))

		grp, ctx := errgroup.WithContext(ctx)

		grp.Go(func() error {
			for {
				// read must be called to get pong messages handeled
				messageType, p, err := ws.ReadMessage()
				if err != nil {
					slog.Warn("read message", "err", err)
					return err
				}
				_ = messageType
				_ = p
			}
			return nil
		})

		grp.Go(func() error {
			pingTicker := time.NewTicker(pingPeriod)
			defer pingTicker.Stop()
			for {
				select {
				case <-pingTicker.C:
					err := ws.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait))
					if err != nil {
						slog.Warn("write ping message", "err", err)
						return err
					}
				}
			}

		})

		if err := grp.Wait(); err != nil {
			return fmt.Errorf("group: %w", err)
		}
		return nil
	}
}

var errorTmpl = template.Must(template.New("base.html").Funcs(template.FuncMap{
	"static": assets.StaticHashFS.HashName,
}).ParseFS(assets.TemplateFS, "template/base.html", "template/error.html"))

func (s *Server) ErrorPage(ctx context.Context, w http.ResponseWriter, r *http.Request, detail, continueURL string) error {

	var b bytes.Buffer
	err := errorTmpl.Execute(&b, map[string]interface{}{
		"Title":       "Error",
		"Detail":      detail,
		"ContinueURL": continueURL,
	})
	if err != nil {
		return err
	}
	if !WriteHTMLResponse(ctx, w, 200, b.Bytes()) {
		return nil
	}
	return nil
}

// WriteJSONResponse writes a json response to the client.
//
// Returns false if the write fails.
func WriteJSONResponse(ctx context.Context, w http.ResponseWriter, statusCode int, value interface{}) bool {

	data, err := json.Marshal(&value)
	if err != nil {
		slog.Error("err", "err", err)
		// InternalWriteError(ctx, w, http.StatusInternalServerError, errors.ErrorResponse{})
		w.WriteHeader(http.StatusInternalServerError)
		return false
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)
	_, err = w.Write(data)
	if err != nil {
		slog.Warn("error writing response", "err", err)
		return false
	}
	return true
}

func WriteHTMLResponse(ctx context.Context, w http.ResponseWriter, statusCode int, body []byte) bool {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)
	_, err := w.Write(body)
	if err != nil {
		slog.Warn("error writing response", "err", err)
		return false
	}
	return true

}
