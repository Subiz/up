package main

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/urfave/cli"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v2"
)

var username, password = os.Getenv("BBUSER"), os.Getenv("BBPASS")

type Version struct {
	Commit  string
	Repo    string
	Branch  string
	Version string
}

func main() {
	app := cli.NewApp()

	app.Version = "0.1.0"
	cli.VersionFlag = cli.BoolFlag{
		Name:  "version, V",
		Usage: "print the version",
	}
	app.Commands = []cli.Command{
		{
			Name:    "update",
			Aliases: []string{"u"},
			Usage:   "update all repo and build lock files",
			Action: func(c *cli.Context) error {
				update()
				return nil
			},
		},
		{
			Name:    "add",
			Aliases: []string{"a"},
			Usage:   "add new service",
			Action: func(c *cli.Context) error {
				return nil
			},
		},
	}

	sort.Sort(cli.FlagsByName(app.Flags))
	sort.Sort(cli.CommandsByName(app.Commands))

	app.Run(os.Args)
}

func update() {
	version, err := ioutil.ReadFile("up.yaml")
	if err != nil || string(version) == "" {
		panic("unable to read ./up.yaml file")
	}

	v := make(map[string]*Version) // version in map format
	if err := yaml.Unmarshal(version, &v); err != nil {
		panic(err)
	}

	mutex := &sync.Mutex{}
	// loop through version
	// try to get original deploy.yaml in repo then merge it with devop
	// modification
	var wg sync.WaitGroup
	outyaml := make([]byte, 0)
	for sname, sver := range v {
		wg.Add(1)
		go func(sname string, sver *Version) {
			defer wg.Done()
			if sver.Commit == "" {
				// get commit
				commit := getLatestCommit(sver.Repo, sver.Branch, username, password)
				if commit == "" {
					panic("no commit found for repo " + sver.Repo + " branch " + sver.Branch)
				}
				sver.Commit = commit
			}
			fmt.Printf("INFO: getting version for service %s at repo %s\n", sname, sver.Repo)
			version := getVersionNumber(sver.Repo, sver.Commit, username, password)
			version = strings.TrimSpace(version)
			fmt.Printf("INFO: getting deployment for service %s at repo %s\n", sname, sver.Repo)
			deploy := getDeployYaml(sver.Repo, sver.Commit, username, password)
			deploy = []byte(strings.Replace(string(deploy), "$VERSION", version, -1))
			moddeploy := readDeployModification(sname)
			moddeploy = []byte(strings.Replace(string(deploy), "$VERSION", version, -1))

			fmt.Printf("INFO: merging deployment for service %s\n", sname)
			merged := mergeYAML(moddeploy, deploy)
			//applyKubernetes(merge)

			mutex.Lock()
			outyaml = append(outyaml, "---\n"...)
			outyaml = append(outyaml, merged...)
			sver.Version = version
			mutex.Unlock()
		}(sname, sver)
	}
	wg.Wait()
	version, err = yaml.Marshal(&v)
	if err != nil {
		panic(err)
	}

	if err := ioutil.WriteFile("up-lock.yaml", version, 0644); err != nil {
		panic(err)
	}
	if err := ioutil.WriteFile("deploy-lock.yaml", outyaml, 0644); err != nil {
		panic(err)
	}
	fmt.Println("up-lock.yaml, deploy-lock.yaml are written")
	fmt.Println("done.")
}

// merge 2 golang struct, x1's props overrides x2's props
func mergeStruct(x1, x2 interface{}) interface{} {
	switch x1 := x1.(type) {
	case map[string]interface{}:
		x2, ok := x2.(map[string]interface{})
		if !ok {
			return x1
		}
		for k, v2 := range x2 {
			if v1, ok := x1[k]; ok {
				x1[k] = mergeStruct(v1, v2)
			} else {
				x1[k] = v2
			}
		}
	case map[interface{}]interface{}:
		x2, ok := x2.(map[interface{}]interface{})
		if !ok {
			return x1
		}
		for k, v2 := range x2 {
			if v1, ok := x1[k]; ok {
				x1[k] = mergeStruct(v1, v2)
			} else {
				x1[k] = v2
			}
		}
	case nil:
		return x2
	default:
		return x1
	}
	return x1
}

func getConfigNameAndKind(config map[interface{}]interface{}) (name, kind string) {
	name = ""
	kind, _ = config["kind"].(string)
	if config["metadata"] == nil {
		return
	}

	metadata, ok := config["metadata"].(map[interface{}]interface{})
	if !ok {
		return
	}

	name, _ = metadata["name"].(string)
	return
}

// merge 2 yaml structs, x1's props override x2's props
// this function loop through all config in a and b (O(n^2))
// very inefficient, but who case about few milliseconds
func mergeYAML(a []byte, b []byte) (outyaml []byte) {
	// split config into multiple config delimited by ---
	asplit := RegSplit(string(a), "(?m:^[-]{3,})")
	bsplit := RegSplit(string(b), "(?m:^[-]{3,})")
	unuseds := make([]string, len(asplit)) // tell if there is some unused configs
	copy(unuseds, asplit)
	for _, cb := range bsplit {
		yamlb, nb, kb := parseConfig(cb)
		ismerged := false           // try to merge ca with cb if matched
		for _, ca := range asplit { // should cache ca
			yamla, na, ka := parseConfig(ca)
			if na != nb || ka != kb {
				continue
			}
			unuseds = removeString(unuseds, ca)
			ret := mergeStruct(yamla, yamlb)
			mergedyaml, err := yaml.Marshal(ret)
			if err != nil {
				panic(err)
			}
			outyaml = append(outyaml, "\n---\n"...)
			outyaml = append(outyaml, mergedyaml...)
			ismerged = true
			break
		}

		if !ismerged { // still keep if not match
			outyaml = append(outyaml, ("\n---\n" + cb)...)
		}
	}

	for _, unused := range unuseds {
		if unused != "" {
			_, name, kind := parseConfig(unused)
			fmt.Printf("WARN: unused config kind %s, name %s\n", kind, name)
		}
	}
	return
}

// parseConfig parse kubernetes config content into yaml object, name of config and kind of config.
func parseConfig(content string) (map[interface{}]interface{}, string, string) {
	y := make(map[interface{}]interface{})
	if err := yaml.Unmarshal([]byte(content), &y); err != nil {
		panic(err)
	}
	name, kind := getConfigNameAndKind(y)
	return y, name, kind
}

/*
func applyKubernetes(deploy []byte) {
	// write deploy to temp file
	tmpfile, err := ioutil.TempFile("", "deploy")
	if err != nil {
		log.Fatal(err)
	}
	defer os.Remove(tmpfile.Name()) // clean up

	if _, err := tmpfile.Write(deploy); err != nil {
		panic(err)
	}
	if err := tmpfile.Close(); err != nil {
		panic(err)
	}

	kinds, names, versions := getKubeConfigVersions(tmpfile.Name())
	for i := range kinds {

	}

	// call shell to apply kubernetes
	cmd := exec.Command("kubectl", "apply", "-f", tmpfile.Name())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	cmd.Start()

	chunk := make([]byte, 10)
	for {
		if _, err := stdout.Read(chunk); err != nil {
			break
		}
		fmt.Print(chunk)
	}
}
*/
func readDeployModification(sname string) []byte {
	data, err := ioutil.ReadFile(sname + ".yaml")
	if err != nil {
		fmt.Printf("INFO: no modification deploy for service %s: %v\n", sname, err)
		return nil
	}
	fmt.Printf("INFO: got modification deploy for service %s\n", sname)
	return data
}

func getLatestCommit(repo, branch, us, pw string) string {
	url := "https://api.bitbucket.org/2.0/repositories/" + repo + "/commits/" + branch + "?page=1&pagelen=2"
	code, body := getHTTP(url, us, pw, nil)
	if code != 200 {
		panic("request to " + url + " not return 200, got " + strconv.Itoa(code))
	}

	return gjson.Get(string(body), "values.0.hash").String()
}

func getVersionNumber(repo, commit, us, pw string) string {
	url := "https://bitbucket.org/" + repo + "/raw/" + commit + "/version"
	code, body := getHTTP(url, us, pw, nil)
	if code != 200 {
		panic("request to " + url + " not return 200, got " + strconv.Itoa(code))
	}
	return string(body)
}

func getDeployYaml(repo, commit, us, pw string) []byte {
	url := "https://bitbucket.org/" + repo + "/raw/" + commit + "/deploy.yaml"
	code, body := getHTTP(url, us, pw, nil)

	if code != 200 {
		panic("request to " + url + " not return 200, got " + strconv.Itoa(code))
	}
	return body
}

// http client
var hclient = &fasthttp.Client{
	MaxConnsPerHost: 100,
}

func getHTTP(fullurl, username, password string, header map[string]string) (int, []byte) {
	timeout := 1 * time.Minute
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(fullurl)
	req.Header.SetMethod("GET")

	for k, v := range header {
		req.Header.Set(k, v)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	req.Header.SetUserAgent("Subiz-Gun/4.012")
	req.Header.Set("Authorization", toBasicAuth(username, password))

	res := fasthttp.AcquireResponse()
	if err := hclient.DoTimeout(req, res, timeout); err != nil {
		panic(err)
	}

	return res.StatusCode(), res.Body()
}

func toBasicAuth(username, password string) string {
	authcode := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return "Basic " + authcode
}

func RegSplit(text string, delimeter string) []string {
	reg := regexp.MustCompile(delimeter)
	indexes := reg.FindAllStringIndex(text, -1)
	laststart := 0
	result := make([]string, len(indexes)+1)
	for i, element := range indexes {
		result[i] = text[laststart:element[0]]
		laststart = element[1]
	}
	result[len(indexes)] = text[laststart:len(text)]
	return result
}

func removeString(s []string, r string) []string {
	for i, v := range s {
		if v == r {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func getKubeConfigVersions(filename string) (kinds, names, versions []string) {
	data, err := exec.Command("kubectl", "get", "-f", filename, "-o", "jsonpath={range .items[*]}{@.metadata.name}{\" \"}{@.kind}{\" \"}{@.metadata.annotations.version}{\"\\n\"}{end}").Output()
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))
	resources := strings.Split(string(data), "\n")
	for _, r := range resources {
		rsplit := strings.Split(r, " ")
		if len(rsplit) < 3 {
			continue
		}
		names = append(names, rsplit[0])
		kinds = append(kinds, rsplit[1])
		versions = append(versions, rsplit[2])
	}
	return
}

func getYamlConfigVersion(content, kind, name string) string {
	configs := RegSplit(content, "(?m:^[-]{3,})")
	for _, c := range configs {
		y, n, k := parseConfig(c)
		if n == name && k == kind {
			if annos, ok := y["annotations"].(map[interface{}]interface{}); ok {
				version, _ := annos["version"].(string)
				return version
			}
		}
	}
	return ""
}

func checkForChangesInVersion(filename, content string) {

}
