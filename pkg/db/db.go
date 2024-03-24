package db

import (
	"cmp"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/rs/xid"
	"github.com/some-programs/battlr/pkg/scanner"
	bolt "go.etcd.io/bbolt"
	"gopkg.in/yaml.v3"
)

var (
	NotFound     = errors.New("not found")
	InvalidScore = errors.New("invalid score")
)

const (
	votesBucketNamePrefix = "votes⊳"
	battlesBucketName     = "battles"
)

type DB struct {
	BoltDB *bolt.DB
}

type Entries []Entry

func (e Entries) SortByScore(scoreMap ScoreMap) {
	slices.SortStableFunc(e, func(a, b Entry) int {
		return cmp.Compare(scoreMap[b.ID], scoreMap[a.ID])
	})
}

func (e Entries) SortByID() {
	slices.SortFunc(e, func(a, b Entry) int {
		return cmp.Compare(a.ID, b.ID)
	})
}

func (e Entries) Shuffle() {

	rnd := rand.New(rand.NewChaCha8([32]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 0}))
	rnd.Shuffle(len(e), func(i, j int) {
		e[i], e[j] = e[j], e[i]
	})
}

func (e Entries) Contains(entry Entry) bool {
	for _, v := range e {
		if entry.ID == v.ID {
			return true
		}
	}
	return false
}

func (e Entries) Places(scoreMap ScoreMap) Places {
	if len(e) == 0 {
		return nil
	}
	e.SortByID()
	e.SortByScore(scoreMap)

	var topPlaces Places
	var currentPlace Entries

	for i, entry := range e {
		if i != 0 && scoreMap[e[i-1].ID] != scoreMap[entry.ID] {
			currentPlace.SortByID()
			currentPlace.Shuffle()
			topPlaces = append(topPlaces, currentPlace)
			currentPlace = []Entry{}
		}

		currentPlace = append(currentPlace, entry)
		if scoreMap[entry.ID] == 0 {
			return topPlaces
		}

	}
	currentPlace.SortByID()
	currentPlace.Shuffle()

	topPlaces = append(topPlaces, currentPlace)

	return topPlaces
}

type Places []Entries

func (p Places) All() Entries {
	var res Entries
	for _, place := range p {
		res = append(res, place...)
	}
	return res
}

func (p Places) Diff(entries Entries) Entries {
	var res Entries
	all := p.All()
	for _, e := range entries {
		if !all.Contains(e) {
			res = append(res, e)
		}
	}
	return res
}

type Battle struct {
	Name      string    `yaml:"name"`
	Entries   Entries   `yaml:"entries"`
	ClosedAt  time.Time `yaml:"closed_at"`
	CreatedAt time.Time `yaml:"crated_at"`
}

func (d Battle) IsVotingOpen() bool {
	return d.ClosedAt.IsZero()
}

func (d *Battle) GetEntryByID(id string) (Entry, bool) {
	for _, e := range d.Entries {
		if id == e.ID {
			return e, true
		}
	}
	return Entry{}, false
}

func (d *Battle) GetEntryByFilename(filename string) (Entry, bool) {
	for _, e := range d.Entries {
		if filename == e.Filename {
			return e, true
		}
	}
	return Entry{}, false
}

type Entry struct {
	ID        string    `yaml:"id"`
	Title     string    `yaml:"title"`
	Author    string    `yaml:"author"`
	Filename  string    `yaml:"filename"`
	CreatedAt time.Time `yaml:"created_at"`
}

// ScoreMap is [entryID]score
type ScoreMap map[string]int

type Votes struct {
	BattleName string    `yaml:"battle"`
	VoterID    string    `yaml:"voter_id"`
	CreatedAt  time.Time `yaml:"created_at"`
	UpdatedAt  time.Time `yaml:"updated_at"`
	Scores     ScoreMap  `yaml:"score"`
}

// SetScore updates the scores map in a way where one score value is uniqe
// among the values.
func (v *Votes) UpdateScore(entryID string, score int) {
	for id, existingScore := range v.Scores {
		if existingScore == score {
			if id == entryID {
				return
			}
			delete(v.Scores, id)
		}
	}
	v.Scores[entryID] = score
	v.UpdatedAt = time.Now()
}

func (db *DB) GetBattle(battleName string) (*Battle, error) {
	var battle *Battle
	err := db.BoltDB.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(battlesBucketName))
		if bucket == nil {
			return NotFound
		}

		var err error
		battle, err = getBattle(bucket, battleName)
		return err
	})
	if err != nil {
		return nil, err
	}
	return battle, nil
}

func (db *DB) OpenBattle(battleName string) error {
	err := db.BoltDB.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(battlesBucketName))
		if bucket == nil {
			return NotFound
		}
		battle, err := getBattle(bucket, battleName)
		if err != nil {
			return err
		}
		battle.ClosedAt = time.Time{}
		return putBattle(bucket, *battle)

	})
	if err != nil {
		return err
	}
	return nil
}

func (db *DB) CloseBattle(battleName string) error {
	err := db.BoltDB.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(battlesBucketName))
		if bucket == nil {
			return NotFound
		}
		battle, err := getBattle(bucket, battleName)
		if err != nil {
			return err
		}
		battle.ClosedAt = time.Now()
		return putBattle(bucket, *battle)

	})
	if err != nil {
		return err
	}
	return nil
}

func (db *DB) GetAllBattles() ([]Battle, error) {
	var battles []Battle

	err := db.BoltDB.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(battlesBucketName))
		if bucket == nil {
			return nil
		}
		if err := bucket.ForEach(func(k, v []byte) error {
			var battle Battle
			if err := yaml.Unmarshal(v, &battle); err != nil {
				return err
			}
			battles = append(battles, battle)
			return nil
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return battles, nil
}

func (db *DB) UpdateBattle(fsBattle scanner.Battle) error {
	err := db.BoltDB.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(battlesBucketName))
		if err != nil {
			return err
		}

		newBattle := Battle{
			Name:      fsBattle.Name,
			CreatedAt: time.Now(),
		}

		var oldBattle Battle
		data := bucket.Get([]byte(fsBattle.Name))
		if data != nil {
			if err := yaml.Unmarshal(data, &oldBattle); err != nil {
				return err
			}
			newBattle.CreatedAt = oldBattle.CreatedAt
			newBattle.ClosedAt = oldBattle.ClosedAt
		}

		var newEntries []Entry
		for _, fsEntry := range fsBattle.Entries {
			newEntry := Entry{
				ID:        xid.New().String(),
				Author:    fsEntry.Author,
				Title:     fsEntry.Title,
				Filename:  fsEntry.Filename,
				CreatedAt: time.Now(),
			}

			prevEntry, ok := oldBattle.GetEntryByFilename(newEntry.Filename)
			if ok {
				newEntry.ID = prevEntry.ID
				newEntry.CreatedAt = prevEntry.CreatedAt
			}
			newEntries = append(newEntries, newEntry)
		}

		newBattle.Entries = newEntries

		slog.Info("storing", "battle", newBattle)
		if err := putBattle(bucket, newBattle); err != nil {
			return err
		}

		return nil
	})
	return err
}

func (db *DB) GetVotes(battleName string, voterID string) (*Votes, error) {
	var votes *Votes
	err := db.BoltDB.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(newVotesBucketKey(battleName))
		if bucket == nil {
			return NotFound
		}

		var err error
		votes, err = getVotes(bucket, battleName, voterID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return votes, nil
}

func (db *DB) GetAllVotes(battleName string) ([]Votes, error) {
	var votes []Votes

	err := db.BoltDB.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(newVotesBucketKey(battleName)))
		if bucket == nil {
			return nil
		}
		if err := bucket.ForEach(func(k, v []byte) error {
			var vote Votes
			if err := yaml.Unmarshal(v, &vote); err != nil {
				return err
			}
			votes = append(votes, vote)
			return nil
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return votes, nil
}

func (db *DB) UpdateVote(battleName string, entryID string, voterID string, score int) error {
	if score < 1 || score > 3 {
		return InvalidScore
	}

	err := db.BoltDB.Update(func(tx *bolt.Tx) error {

		battlesBucket := tx.Bucket([]byte(battlesBucketName))

		if battlesBucket == nil {
			return NotFound
		}

		battle, err := getBattle(battlesBucket, battleName)
		if err != nil {
			return err
		}

		_, ok := battle.GetEntryByID(entryID)
		if !ok {
			return NotFound
		}

		votesBucket, err := tx.CreateBucketIfNotExists(newVotesBucketKey(battleName))
		if err != nil {
			return err
		}

		now := time.Now()
		votes, err := getVotes(votesBucket, battleName, voterID)
		if err != nil {
			return err
		}

		if votes == nil {
			votes = &Votes{
				VoterID:   voterID,
				CreatedAt: now,
			}
		}

		if votes.Scores == nil {
			votes.Scores = make(map[string]int)
		}
		votes.UpdateScore(entryID, score)

		if err := putVotes(votesBucket, *votes); err != nil {
			return err
		}

		return nil
	})

	return err

}

func (db *DB) RemoveVotes(battleName string, voterID string) error {

	err := db.BoltDB.Update(func(tx *bolt.Tx) error {

		battlesBucket := tx.Bucket([]byte(battlesBucketName))

		if battlesBucket == nil {
			return NotFound
		}

		battle, err := getBattle(battlesBucket, battleName)
		if err != nil {
			return err
		}

		votesBucket, err := tx.CreateBucketIfNotExists(newVotesBucketKey(battle.Name))
		if err != nil {
			return err
		}

		if err := votesBucket.Delete([]byte(voterID)); err != nil {
			return err
		}

		return nil
	})

	return err

}

func getBattle(bucket *bolt.Bucket, battleName string) (*Battle, error) {
	return retreiveYaml[Battle](bucket, []byte(battleName))
}

func putBattle(bucket *bolt.Bucket, battle Battle) error {
	return storeYaml(bucket, []byte(battle.Name), battle)
}

func getVotes(bucket *bolt.Bucket, battleName string, voterID string) (*Votes, error) {
	return retreiveYaml[Votes](bucket, []byte(voterID))
}

func putVotes(bucket *bolt.Bucket, votes Votes) error {
	return storeYaml(bucket, []byte(votes.VoterID), votes)
}

func newVotesBucketKey(battleName string) []byte {
	key := []byte(votesBucketNamePrefix)
	key = append(key, []byte(battleName)...)
	return key
}

func retreiveYaml[T any](bucket *bolt.Bucket, key []byte) (*T, error) {
	data := bucket.Get(key)
	if data == nil {
		return nil, nil
	}
	var instance T
	if err := yaml.Unmarshal(data, &instance); err != nil {
		return nil, err
	}
	return &instance, nil
}

func storeYaml(bucket *bolt.Bucket, key []byte, instance any) error {
	data, err := yaml.Marshal(&instance)
	if err != nil {
		return err
	}
	return bucket.Put(key, data)
}

func SumScores(scores []Votes) map[string]int {
	res := make(map[string]int)
	for _, s := range scores {
		for k, v := range s.Scores {
			res[k] = res[k] + v
		}
	}
	return res
}
