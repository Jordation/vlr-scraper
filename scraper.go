package main

// fuzz the number i.e. vlr.gg/numbers
// matches are in a set, between them are invalid posts
// i.e. vlr.gg/100 -> /120 all matches then posts
// account for missing date -> check if parent event has date
// handle "\u00a0" for bad datasets
import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	s "strings"
	"time"

	stat_db "github.com/Jordation/go-api/server/db"
	"github.com/gocolly/colly/v2"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	Comb = 0
	Atk  = 1
	Def  = 2
)

type SharedMatchData struct {
	Url       string
	Patch     string
	MatchDate time.Time
	Event     string
	Teams     []string
}

type MapData struct {
	TeamScores []uint
	Map        string
}
type PlayerData struct {
	Player string
	Agent  string
	Stats  [][]string
}

type MatchDataset struct {
	Shared  SharedMatchData
	Maps    []MapData
	Players []PlayerData
}

/*
//////////////////////////////////////
			HELPERS
//////////////////////////////////////
*/

func strip(str string) string {
	return s.ReplaceAll(s.ReplaceAll(s.ReplaceAll(str, "\t", ""), "\n", ""), "%", "")
}

func Contains[T comparable](s []T, e T) bool {
	for _, v := range s {
		if e == v {
			return true
		}
	}
	return false
}

func sumArr[T int | uint](arr []T) T {
	var c T
	for _, v := range arr {
		c += v
	}
	return c
}
func GetMapFacts(scores []uint, teams []string) (string, uint, uint) {
	var (
		h      = len(scores) / 2
		t1     = scores[:h]
		t2     = scores[h:]
		AtkR   = t1[1] + t2[0]
		DefR   = t1[0] + t2[1]
		winner string
	)

	if sumArr(t1) > sumArr(t2) {
		winner = teams[0]
	} else {
		winner = teams[1]
	}
	return winner, AtkR, DefR
}

func GetComp(data []PlayerData) (res string) {
	sort.Slice(data, func(i, j int) bool {
		return data[i].Agent < data[j].Agent
	})

	for _, v := range data {
		res += v.Agent + ","
	}
	res = s.TrimRight(res, ",")
	return res
}

func SniffMatch(url string, c *colly.Collector) string {
	str := ""
	c.OnHTML("div.match-header-vs>div>div:first-child", func(e *colly.HTMLElement) {
		str = strip(e.Text)
	})

	c.Visit(url)
	c.Wait()
	if str != "final" {
		return ""
	}
	return url
}

/*
//////////////////////////////////////
			HELPERS END
//////////////////////////////////////
*/

func Scrape(url string, c *colly.Collector) (MatchDataset, error) {
	var (
		PatchExp, _ = regexp.Compile(`\w+.\d+\.\d+`)
		Players     = make([]PlayerData, 0)
		Maps        = make([]MapData, 0)
		rawDate     = ""
		SharedData  SharedMatchData
	)

	SharedData.Url = url
	//get header data
	c.OnHTML("div.wf-card.match-header", func(e *colly.HTMLElement) {
		rawPatch := e.ChildText("div.match-header-super>div>div>div:nth-child(3)>div:first-child")
		SharedData.Patch = PatchExp.FindString(rawPatch)
		SharedData.Event = e.ChildText("div.match-header-super>div>a>div>div:first-child")
		rawDate = e.ChildAttr("div.match-header-super>div>div>div.moment-tz-convert", "data-utc-ts")

		// team1, team2
		e.ForEach("div.match-header-vs>a>div>div.wf-title-med", func(_ int, e2 *colly.HTMLElement) {
			SharedData.Teams = append(SharedData.Teams, (strip(e2.Text)))
		})
	})
	c.OnHTML("div.vm-stats-game", func(e *colly.HTMLElement) {
		if e.DOM.HasClass("mod-active") {
			return
		}
		var tempMap MapData
		teamScores := make([]string, 0)
		tempMap.Map = strip(s.Split(e.ChildText("div.map>div>span"), "PICK")[0])
		e.ForEach("div.team>div>span", func(_ int, e2 *colly.HTMLElement) {
			teamScores = append(teamScores, strip(e2.Text))
		})
		tempMap.TeamScores = statToUint(teamScores)
		Maps = append(Maps, tempMap)

		e.ForEach("tr", func(_ int, e *colly.HTMLElement) {
			var tempPlayer PlayerData
			e.ForEach("td", func(_ int, e2 *colly.HTMLElement) {
				if e2.DOM.HasClass("mod-kd-diff") ||
					e2.DOM.HasClass("mod-fk-diff") {
					return
				} else if e2.DOM.HasClass("mod-player") {
					tempPlayer.Player = strip(e2.ChildText("div>a>div.text-of"))
				} else if e2.DOM.HasClass("mod-agents") {
					tempPlayer.Agent = strip(e2.ChildAttr("div>span>img", "title"))
				} else {
					stats := make([]string, 0)
					e2.ForEach("span.side", func(_ int, e3 *colly.HTMLElement) {
						stats = append(stats, strip(e3.Text))
					})
					tempPlayer.Stats = append(tempPlayer.Stats, stats)
				}
			})
			if tempPlayer.Player != "" {
				Players = append(Players, tempPlayer)
			}
		})
	})
	c.Visit(url)
	c.Wait()
	// prolly do this elsewhere so its not so ugly
	pTime, err := time.Parse("2006-01-02 15:04:05", rawDate)
	if err != nil {
		return MatchDataset{}, fmt.Errorf("no date or patch, cannot evaluate time of match, aborting %v", url)
	}
	SharedData.MatchDate = pTime

	for _, v := range Players {
		for _, v2 := range v.Stats {
			if Contains(v2, "\u00a0") {
				return MatchDataset{}, fmt.Errorf("page %v contains busted stats, aborting", url)
			}
		}
	}

	if len(Maps) == 1 {
		return MatchDataset{}, fmt.Errorf("not recording bo1's, aborting %v", url)
	}

	for m := range Maps {
		if sumArr(Maps[m].TeamScores) < 13 {
			return MatchDataset{}, fmt.Errorf("some busted data here %v, aborting", url)
		}
	}

	return MatchDataset{
		Shared:  SharedData,
		Maps:    Maps,
		Players: Players,
	}, nil
}

func CreateMapEntries(maps []MapData, shared SharedMatchData) []stat_db.Map {
	match_uuid := uuid.New()
	res := make([]stat_db.Map, 0)
	for _, v := range maps {
		winner, ar, dr := GetMapFacts(v.TeamScores, shared.Teams)
		res = append(res, stat_db.Map{
			// Fills all values except those generated by orm, players, comps
			MatchUUID:  match_uuid,
			MatchDate:  shared.MatchDate,
			Team1:      shared.Teams[0],
			Team2:      shared.Teams[1],
			Winner:     winner,
			AtkRndsWon: ar,
			DefRndsWon: dr,
			Map:        v.Map,
		})
	}
	return res
}
func FillTeamDataset(data []PlayerData, Map, Team string) []stat_db.PlayerStat {
	res := make([]stat_db.PlayerStat, 0)
	for _, player := range data {
		var (
			statSet  = make([]stat_db.PlayerStat, 3)
			intStats = make([][]uint, 0)
			ratings  = make([]float64, 0)
			combStat = make([]uint, 0)
			atkStat  = make([]uint, 0)
			defStat  = make([]uint, 0)
		)
		for i, stat := range player.Stats {
			if i == 0 {
				ratings = append(ratings, statToFloat64(stat)...)
				continue
			}
			intStats = append(intStats, statToUint(stat))
		}

		for _, group := range intStats {
			combStat = append(combStat, group[Comb])
			atkStat = append(atkStat, group[Atk])
			defStat = append(defStat, group[Def])
		}
		statSet[0].LoadValues(player.Player, player.Agent, Team, Map, "C", combStat, ratings[Comb])
		statSet[1].LoadValues(player.Player, player.Agent, Team, Map, "A", atkStat, ratings[Atk])
		statSet[2].LoadValues(player.Player, player.Agent, Team, Map, "D", defStat, ratings[Def])
		res = append(res, statSet...)
	}
	return res
}

func CreatePlayerStatEntries(data MatchDataset) ([]stat_db.PlayerStat, error) {
	res := make([]stat_db.PlayerStat, 0)
	if len(data.Players) != len(data.Maps)*10 {
		return nil, fmt.Errorf("player count does not align with map count")
	}
	for i, v := range data.Maps {
		i *= 5
		j := i + 5
		t1 := FillTeamDataset(data.Players[i:j], v.Map, data.Shared.Teams[0])
		t2 := FillTeamDataset(data.Players[j:j+5], v.Map, data.Shared.Teams[1])
		res = append(res, append(t1, t2...)...)
	}

	return res, nil
}

func CreateCompEntries(Maps []stat_db.Map, pdata []PlayerData, data SharedMatchData) ([]stat_db.AgentComp, error) {
	comps := make([]stat_db.AgentComp, 0)
	if len(pdata) < 20 {
		return nil, fmt.Errorf("not enough players")
	}
	var t1w_status, t2w_status bool
	for i, v := range Maps {
		i *= 10
		j := i + 5

		if v.Winner == data.Teams[0] {
			t1w_status = true
			t2w_status = false
		} else {
			t1w_status = false
			t2w_status = true
		}

		t1c := stat_db.AgentComp{
			Map:      v.Map,
			Comp:     GetComp(pdata[i:j]),
			PickedBy: data.Teams[0],
			Won:      t1w_status,
		}

		t2c := stat_db.AgentComp{
			Map:      v.Map,
			Comp:     GetComp(pdata[j : j+5]),
			PickedBy: data.Teams[1],
			Won:      t2w_status,
		}
		comps = append(comps, t1c, t2c)
	}

	return comps, nil
}

func GetDbEntities(data MatchDataset) ([]stat_db.Map, error) {
	maps := CreateMapEntries(data.Maps, data.Shared)
	comps, err := CreateCompEntries(maps, data.Players, data.Shared)
	if err != nil {
		return nil, err
	}
	players, err := CreatePlayerStatEntries(data)
	if err != nil {
		return nil, err
	}
	for i := range maps {
		j := i * 2
		h := i * 30
		maps[i].LinkChildren(players[h:h+30], comps[j:j+2])
	}
	return maps, nil
}

func CreateDbEntries(data MatchDataset, db *gorm.DB) error {
	Link := stat_db.HitLink{
		Link: data.Shared.Url,
	}
	if err := db.Create(&Link).Error; err != nil {
		return err
	}
	maps, err := GetDbEntities(data)
	if err != nil {
		return err
	}

	event := stat_db.Event{
		EventName: data.Shared.Event,
		Maps:      maps,
	}

	if err := db.Create(&event).Error; err != nil {
		var foundEvent stat_db.Event
		db.Find(&stat_db.Event{}, "event_name = ?", data.Shared.Event).Scan(&foundEvent)
		for _, m := range maps {
			m.EventID = foundEvent.ID
			if err := db.Create(&m).Error; err != nil {
				log.Fatal(err)
			}
		}
	}
	return nil
}

func statToFloat64(stats []string) []float64 {
	res := make([]float64, 0)
	for _, v := range stats {
		newVal, err := strconv.ParseFloat(s.TrimRight(v, "%"), 64)
		if err != nil {
			return nil
		}
		res = append(res, newVal)
	}
	return res
}
func statToUint(stats []string) []uint {
	res := make([]uint, 0)
	for _, v := range stats {
		newVal, err := strconv.Atoi(s.TrimRight(v, "%"))
		if err != nil {
			return nil
		}
		res = append(res, uint(newVal))
	}
	return res
}
