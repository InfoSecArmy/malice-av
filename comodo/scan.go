package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/crackcomm/go-clitable"
	"github.com/fatih/structs"
	"github.com/levigross/grequests"
	"github.com/maliceio/go-plugin-utils/database/elasticsearch"
	"github.com/maliceio/go-plugin-utils/utils"
	"github.com/parnurzeal/gorequest"
	"github.com/urfave/cli"
)

// Version stores the plugin's version
var Version string

// BuildTime stores the plugin's build time
var BuildTime string

const (
	name     = "comodo"
	category = "av"
)

type pluginResults struct {
	ID   string      `json:"id" structs:"id,omitempty"`
	Data ResultsData `json:"comodo" structs:"comodo"`
}

// Comodo json object
type Comodo struct {
	Results ResultsData `json:"comodo"`
}

// ResultsData json object
type ResultsData struct {
	Infected bool   `json:"infected" structs:"infected"`
	Result   string `json:"result" structs:"result"`
	Engine   string `json:"engine" structs:"engine"`
	Updated  string `json:"updated" structs:"updated"`
}

// ParseComodoOutput convert comodo output into ResultsData struct
func ParseComodoOutput(comodoout string) ResultsData {

	comodo := ResultsData{Infected: false, Engine: "1.1"}
	// EXAMPLE OUTPUT:
	// -----== Scan Start ==-----
	// /malware/EICAR ---> Found Virus, Malware Name is Malware
	// -----== Scan End ==-----
	// Number of Scanned Files: 1
	// Number of Found Viruses: 1
	lines := strings.Split(comodoout, "\n")

	// Extract Virus string
	// for _, line := range lines {
	if len(lines[1]) != 0 {
		if strings.Contains(lines[1], "Found Virus") {
			result := extractVirusName(lines[1])
			if len(result) != 0 {
				comodo.Result = result
				comodo.Infected = true
				return comodo
			}
			fmt.Println("[ERROR] Virus name extracted was empty: ", result)
			os.Exit(2)
		}
	}
	// }
	comodo.Updated = getUpdatedDate()

	return comodo
}

// extractVirusName extracts Virus name from scan results string
func extractVirusName(line string) string {
	keyvalue := strings.Split(line, "is")
	return strings.TrimSpace(keyvalue[1])
}

func getUpdatedDate() string {
	if _, err := os.Stat("/opt/malice/UPDATED"); os.IsNotExist(err) {
		return BuildTime
	}
	updated, err := ioutil.ReadFile("/opt/malice/UPDATED")
	utils.Assert(err)
	return string(updated)
}

func printStatus(resp gorequest.Response, body string, errs []error) {
	fmt.Println(resp.Status)
}

func parseUpdatedDate(date string) string {
	layout := "200601021504"
	t, _ := time.Parse(layout, date)
	return fmt.Sprintf("%d%02d%02d", t.Year(), t.Month(), t.Day())
}

func printMarkDownTable(comodo Comodo) {

	fmt.Println("#### Comodo")
	table := clitable.New([]string{"Infected", "Result", "Engine", "Updated"})
	table.AddRow(map[string]interface{}{
		"Infected": comodo.Results.Infected,
		"Result":   comodo.Results.Result,
		"Engine":   comodo.Results.Engine,
		"Updated":  comodo.Results.Updated,
	})
	table.Markdown = true
	table.Print()
}

func updateAV() error {
	fmt.Println("Updating Comodo...")
	response, err := grequests.Get("http://download.comodo.com/av/updates58/sigs/bases/bases.cav", nil)
	if err != nil {
		return err
	}

	if response.Ok != true {
		log.Println("Request did not return OK")
	}

	if err := response.DownloadToFile("/opt/COMODO/scanners/bases.cav"); err != nil {
		log.Println("Unable to download file: ", err)
	}
	// Update UPDATED file
	t := time.Now().Format("20060102")
	err = ioutil.WriteFile("/opt/malice/UPDATED", []byte(t), 0644)
	return err
}

var appHelpTemplate = `Usage: {{.Name}} {{if .Flags}}[OPTIONS] {{end}}COMMAND [arg...]

{{.Usage}}

Version: {{.Version}}{{if or .Author .Email}}

Author:{{if .Author}}
  {{.Author}}{{if .Email}} - <{{.Email}}>{{end}}{{else}}
  {{.Email}}{{end}}{{end}}
{{if .Flags}}
Options:
  {{range .Flags}}{{.}}
  {{end}}{{end}}
Commands:
  {{range .Commands}}{{.Name}}{{with .ShortName}}, {{.}}{{end}}{{ "\t" }}{{.Usage}}
  {{end}}
Run '{{.Name}} COMMAND --help' for more information on a command.
`

func main() {
	cli.AppHelpTemplate = appHelpTemplate
	app := cli.NewApp()
	app.Name = "comodo"
	app.Author = "blacktop"
	app.Email = "https://github.com/blacktop"
	app.Version = Version + ", BuildTime: " + BuildTime
	app.Compiled, _ = time.Parse("20060102", BuildTime)
	app.Usage = "Malice Comodo AntiVirus Plugin"
	var elasitcsearch string
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "verbose, V",
			Usage: "verbose output",
		},
		cli.BoolFlag{
			Name:  "table, t",
			Usage: "output as Markdown table",
		},
		cli.BoolFlag{
			Name:   "post, p",
			Usage:  "POST results to Malice webhook",
			EnvVar: "MALICE_ENDPOINT",
		},
		cli.BoolFlag{
			Name:   "proxy, x",
			Usage:  "proxy settings for Malice webhook endpoint",
			EnvVar: "MALICE_PROXY",
		},
		cli.StringFlag{
			Name:        "elasitcsearch",
			Value:       "",
			Usage:       "elasitcsearch address for Malice to store results",
			EnvVar:      "MALICE_ELASTICSEARCH",
			Destination: &elasitcsearch,
		},
	}
	app.Commands = []cli.Command{
		{
			Name:    "update",
			Aliases: []string{"u"},
			Usage:   "Update virus definitions",
			Action: func(c *cli.Context) error {
				return updateAV()
			},
		},
	}
	app.Action = func(c *cli.Context) error {
		path, err := filepath.Abs(c.Args().First())
		utils.Assert(err)

		if _, err := os.Stat(path); os.IsNotExist(err) {
			utils.Assert(err)
		}
		if c.Bool("verbose") {
			log.SetLevel(log.DebugLevel)
		}

		comodo := Comodo{
			Results: ParseComodoOutput(utils.RunCommand("/opt/COMODO/cmdscan", "-vs", path)),
		}

		// upsert into Database
		elasticsearch.InitElasticSearch()
		elasticsearch.WritePluginResultsToDatabase(elasticsearch.PluginResults{
			ID:       utils.Getopt("MALICE_SCANID", utils.GetSHA256(path)),
			Name:     name,
			Category: category,
			Data:     structs.Map(comodo.Results),
		})

		if c.Bool("table") {
			printMarkDownTable(comodo)
		} else {
			comodoJSON, err := json.Marshal(comodo)
			utils.Assert(err)
			if c.Bool("post") {
				request := gorequest.New()
				if c.Bool("proxy") {
					request = gorequest.New().Proxy(os.Getenv("MALICE_PROXY"))
				}
				request.Post(os.Getenv("MALICE_ENDPOINT")).
					Set("Task", path).
					Send(comodoJSON).
					End(printStatus)
			}
			fmt.Println(string(comodoJSON))
		}
		return nil
	}

	err := app.Run(os.Args)
	utils.Assert(err)
}
