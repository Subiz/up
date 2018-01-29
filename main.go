package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	toml "github.com/BurntSushi/toml"
	"github.com/thanhpk/stringf"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"errors"
)

const ServiceCachePath = "./services"
const ConfigPath = ".up"

type ByName []Service

func (n ByName) Len() int           { return len(n) }
func (n ByName) Swap(i, j int)      { n[i], n[j] = n[j], n[i] }
func (n ByName) Less(i, j int) bool { return n[i].Name < n[j].Name }

type Config struct {
	Kind, Name, Content string
}

type ByKindAndName []Config

func (n ByKindAndName) Len() int      { return len(n) }
func (n ByKindAndName) Swap(i, j int) { n[i], n[j] = n[j], n[i] }
func (n ByKindAndName) Less(i, j int) bool {
	if n[i].Name == n[j].Name {
		return n[i].Kind < n[j].Kind
	}
	return n[i].Name < n[j].Name
}

type Service struct {
	Name            string
	Commit          string
	Version         int
	Build           string
	Up              string
	Run             map[interface{}]interface{}
	commit          string
}

type Version struct {
	Commit  string
	Repo    string
	Branch  string
	Version string
}

type UpConfig struct {
	Bbuser     string `toml:"bitbucket_user"`
	Bbpass     string `toml:"bitbucket_pass"`
	Stag       string `toml:"stag"`
	Prod       string `toml:"prod"`
	Dev        string `toml:"dev"`
}

var gconfig UpConfig

func getHomeDir() string {
	return strings.TrimSpace(os.Getenv("HOME"))
}

func loadUpConfig() {
	blob, _ := ioutil.ReadFile(getHomeDir() + "/" + ConfigPath + "/ignoreme.toml")
	if _, err := toml.Decode(string(blob), &gconfig); err != nil {
		fmt.Println("WARN: config file error", err)
	}
}

func config(c *cli.Context) error {
	name, value := c.Args().Get(0), c.Args().Get(1)
	switch name {
	case "bitbucket_user":
		gconfig.Bbuser = value
		tryLoginBb()
	case "bitbucket_pass":
		gconfig.Bbpass = value
		tryLoginBb()
	case "stag":
		gconfig.Stag = value
	case "prod":
		gconfig.Prod = value
	case "dev":
		gconfig.Dev = value
	case "clear":
		gconfig = UpConfig{}
	case "get":
		fmt.Printf("stag: %s\nprod: %s\ndev %s\n", gconfig.Stag, gconfig.Prod, gconfig.Dev)
		return nil
	default:
		fmt.Printf("unknown config")
		return nil
	}
	saveUpConfig()
	fmt.Println("done.")
	return nil
}

func saveUpConfig() {
	buf := new(bytes.Buffer)
	if err := toml.NewEncoder(buf).Encode(gconfig); err != nil {
		panic(err)
	}
	fmt.Println(getHomeDir()+"/"+ConfigPath)
	os.Mkdir(getHomeDir()+"/"+ConfigPath, 0777)
	if err := ioutil.WriteFile(getHomeDir()+"/"+ConfigPath+"/ignoreme.toml", buf.Bytes(), 0644); err != nil {
		panic(err)
	}
}

func tryLoginBb() {
	if gconfig.Bbpass == "" || gconfig.Bbuser == "" {
		return
	}

	url := "https://api.bitbucket.org/1.0/user"
	code, body := getHTTP(url, gconfig.Bbuser, gconfig.Bbpass, nil)
	if code != 200 {
		fmt.Printf("ERR: cant login, got code %d\n", code)
		return
	}

	fmt.Printf("welcome %s.\n", gjson.Get(string(body), "user.display_name").String())
}

func main() {
	loadUpConfig()
	app := cli.NewApp()
	app.Version = "0.2.9"
	cli.VersionFlag = cli.BoolFlag{
		Name:  "version, V",
		Usage: "print the version",
	}
	app.Commands = []cli.Command{
		{
			Name: "info",
			Aliases: []string{"i"},
			Usage: "get info of the service",
			Action: func(c *cli.Context) error {
				info(c)
				return nil
			},
		},
		{
			Name:  "config",
			Usage: "set config: bitbucket_user, bitbucket_pass, stag, prod, dev",
			Action: func(c *cli.Context) error {
				return config(c)
			},
		},
		{
			Name:    "upgrade",
			Aliases: []string{"u"},
			Usage:   "fetch for new version of all service into up-lock.yaml",
			Action: func(c *cli.Context) error {
				upgrade()
				return nil
			},
		},
		{
			Name:    "merge",
			Aliases: []string{"m"},
			Usage:   "merge all deployment file and its modification",
			Action: func(c *cli.Context) error {
				merge()
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
		{
			Name:    "build",
			Aliases: []string{"b", "rebuild"},
			Usage:   "run build script, increase version",
			Action: func(c *cli.Context) error {
				build()
				return nil
			},
		},
		{
			Name:  "inc",
			Usage: "run up script",
			Action: inc,
		},
		{
			Name:  "up",
			Usage: "run up script",
			Action: up,
		},
		{
			Name:  "deploy",
			Usage: "build and deploy to kubernetes dev environment",
			Action: deploy,
		},
		{
			Name:  "run",
			Usage: "exec command defined in run section",
			Action: run,
		},
		{
			Name:  "init",
			Usage: "initialize a service",
		},
		{
			Name:  "config",
			Usage: "config environment",
		},
	}

	sort.Sort(cli.FlagsByName(app.Flags))
	sort.Sort(cli.CommandsByName(app.Commands))

	app.Run(os.Args)
}

func printServices(services []Service) {
	sort.Sort(ByName(services))
	fmt.Println("--")
	for _, s := range services {
		fmt.Printf("%s %s #%d\n", s.Commit[:7], s.Name, s.Version)
	}
	fmt.Printf("total %d services.\n", len(services))
}

func sortDeployment(dep []byte) []byte {
	depsplit := RegSplit(string(dep), "(?m:^[-]{3,})")
	configs := make([]Config, 0)
	for _, config := range depsplit {
		config = strings.TrimSpace(config)
		if config == "" {
			continue
		}
		_, name, kind := parseConfig(config)
		configs = append(configs, Config{
			Name:    name,
			Kind:    kind,
			Content: config,
		})
	}
	sort.Sort(ByKindAndName(configs))

	depsplit = make([]string, 0)
	for _, config := range configs {
		depsplit = append(depsplit, config.Content)
	}
	return []byte(strings.Join(depsplit, "\n---\n"))
}

func saveDeploy(name string, deploy []byte) {
	_ = os.Mkdir(ServiceCachePath, 0777)
	if err := ioutil.WriteFile(ServiceCachePath+"/"+name+".yaml", deploy, 0644); err != nil {
		panic(err)
	}
}

func loadDeploy(name string) []byte {
	deploy, err := ioutil.ReadFile(ServiceCachePath + "/" + name + ".yaml")
	if err != nil {
		panic(err)
	}
	return deploy
}

func checkLoginBb() {
	if gconfig.Bbuser == "" || gconfig.Bbpass == "" {
		fmt.Println("look like you haven't login to bitbucket yet")
		fmt.Println("try")
		fmt.Println("up config set bitbucket_user <YOURBITBUCKETUSER>")
		fmt.Println("up config set bitbucket_pass <YOURBITBUCKETPASS>")
		fmt.Println("to login to bitbucket")
	}
}
func upgrade() {
	checkLoginBb()
	version, err := ioutil.ReadFile("up.yaml")
	if err != nil || string(version) == "" {
		panic("unable to read ./up.yaml file")
	}

	v := make(map[string]*Version) // version in map format
	if err := yaml.Unmarshal(version, &v); err != nil {
		panic(err)
	}

	outServices := make([]Service, 0)

	mutex := &sync.Mutex{}
	var wg sync.WaitGroup
	for sname, sver := range v {
		wg.Add(1)
		go func(sname string, sver *Version) {
			defer wg.Done()
			if sver.Commit == "" {
				// get commit
				commit := getLatestCommit(sver.Repo, sver.Branch, gconfig.Bbuser, gconfig.Bbpass)
				if commit == "" {
					panic("no commit found for repo " + sver.Repo + " branch " + sver.Branch)
				}
				sver.Commit = commit
			}
			fmt.Printf("INFO: fetching repo %s (%s)\n", sver.Repo, sver.Commit[:7])
			service := getService(sver.Repo, sver.Commit, gconfig.Bbuser, gconfig.Bbpass)
			version := strconv.Itoa(service.Version)
			deploy := getDeployYaml(sver.Repo, sver.Commit, gconfig.Bbuser, gconfig.Bbpass)

			fmt.Printf("INFO: save deployment for service %s at %s/%s.yaml\n", service.Name, ServiceCachePath, service.Name)
			saveDeploy(service.Name, deploy)

			mutex.Lock()
			service.Commit = sver.Commit
			outServices = append(outServices, service)
			sver.Version = version
			mutex.Unlock()
		}(sname, sver)
	}
	wg.Wait()
	version, err = yaml.Marshal(&v)
	if err != nil {
		panic(err)
	}

	printServices(outServices)

	if err := ioutil.WriteFile("up-lock.yaml", version, 0644); err != nil {
		panic(err)
	}
	fmt.Println("up-lock.yaml are written")
}

func merge() {
	version, err := ioutil.ReadFile("up-lock.yaml")
	if err != nil || string(version) == "" {
		panic("unable to read ./up-lock.yaml")
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
			deploy := loadDeploy(sname)

			deploy = []byte(compile(string(deploy), sver.Version, sname, sver.Commit[:7]))
			moddeploy := readDeployModification(sname)
			moddeploy = []byte(compile(string(moddeploy), sver.Version, sname, sver.Commit[:7]))

			fmt.Printf("INFO: merging service %s (#%s)\n", sname, sver.Version)
			merged := mergeYAML(moddeploy, deploy)
			merged = addVersionAnnotation(merged, sver.Version, sname)
			mutex.Lock()
			outyaml = append(outyaml, "---\n"...)
			outyaml = append(outyaml, merged...)
			mutex.Unlock()
		}(sname, sver)
	}
	wg.Wait()

	outyaml = sortDeployment(outyaml)
	if err := ioutil.WriteFile("deploy-lock.yaml", outyaml, 0644); err != nil {
		panic(err)
	}
	fmt.Println("deploy-lock.yaml are written")
}

func mergeNamedArray(x1, x2 []interface{}) interface{} {
	out := make([]map[interface{}]interface{}, 0)
	for _, e1 := range x1 {
		e1, ok := e1.(map[interface{}]interface{})
		if !ok {
			return x1
		}
		name1, ok := e1["name"]
		if !ok { // only merge array of name
			return x1
		}
		// try to find x2 matching name
		found := false
		for i, e2 := range x2 {
			e2, ok := e2.(map[interface{}]interface{})
			if !ok {
				return x1
			}

			name2, ok := e2["name"]
			if !ok {
				return x1
			}
			if name1 == name2 { // great, now merge
				found = true
				x2 = append(x2[:i], x2[i+1:]...)
				out = append(out, mergeStruct(e1, e2).(map[interface{}]interface{}))
				break
			}
		}
		if !found {
			out = append(out, e1)
		}
	}
	// add all remaining x2 elements to out
	for _, e := range x2 {
		e, ok := e.(map[interface{}]interface{})
		if !ok {
			break
		}
		out = append(out, e)
	}
	return out
}

// merge 2 golang struct, x1's props overrides x2's props
func mergeStruct(x1, x2 interface{}) interface{} {
	switch x1 := x1.(type) {
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
	case []interface{}:
		x2, ok := x2.([]interface{})
		if !ok {
			return x1
		}
		return mergeNamedArray(x1, x2)
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

func addVersionAnnotation(inyaml []byte, version, service string) (outyaml []byte) {
	// split config into multiple config delimited by ---
	split := RegSplit(string(inyaml), "(?m:^[-]{3,})")
	for _, config := range split {
		config = strings.TrimSpace(config)
		if config == "" {
			continue
		}
		y := make(map[interface{}]interface{})
		if err := yaml.Unmarshal([]byte(config), &y); err != nil {
			panic(err)
		}
		metadata, _ := y["metadata"].(map[interface{}]interface{})
		if metadata == nil {
			metadata = make(map[interface{}]interface{})
			y["metadata"] = metadata
		}

		annotations, _ := metadata["annotations"].(map[interface{}]interface{})
		if annotations == nil {
			annotations = make(map[interface{}]interface{})
			metadata["annotations"] = annotations
		}

		annotations["version"] = version
		annotations["service"] = service
		versionedyaml, err := yaml.Marshal(y)
		if err != nil {
			panic(err)
		}
		outyaml = append(outyaml, "\n---\n"...)
		outyaml = append(outyaml, versionedyaml...)
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

func kube(deploy []byte) {
	// write deploy to temp file
	tmpfile, err := ioutil.TempFile("", "deploy")
	if err != nil {
		panic(err)
	}
	defer os.Remove(tmpfile.Name()) // clean up

	if _, err := tmpfile.Write(deploy); err != nil {
		panic(err)
	}
	if err := tmpfile.Close(); err != nil {
		panic(err)
	}

	var changedService = make(map[string]bool)
	kinds, names, versions, services := getKubeConfigVersions(tmpfile.Name())
	for i := range kinds {
		vers, _ := getYamlConfigVersion(string(deploy), kinds[i], names[i])
		if vers == versions[i] {
			changedService[services[i]] = true
		}
	}

	// call shell to apply kubernetes
	cmd := exec.Command("kubectl", "apply", "-f", tmpfile.Name())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
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

func readDeployModification(sname string) []byte {
	data, err := ioutil.ReadFile(sname + ".yaml")
	if err != nil {
		fmt.Printf("INFO: no modification deploy for service %s: %v\n", sname, err)
		return nil
	}
	//fmt.Printf("INFO: got modification deploy for service %s\n", sname)
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

func getService(repo, commit, us, pw string) Service {
	url := "https://bitbucket.org/" + repo + "/raw/" + commit + "/service.yaml"
	code, body := getHTTP(url, us, pw, nil)
	if code != 200 {
		panic("request to " + url + " not return 200, got " + strconv.Itoa(code))
	}

	s := Service{}
	if err := yaml.Unmarshal(body, &s); err != nil {
		panic(err)
	}
	return s
}

func readDeployYaml() string {
	data, _ := ioutil.ReadFile("deploy.yaml")
	return string(data)
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

func getKubeConfigVersions(filename string) (kinds, names, versions, services []string) {
	data, err := exec.Command("kubectl", "get", "-f", filename, "-o", "jsonpath={range .items[*]}{@.metadata.name}{\" \"}{@.kind}{\" \"}{@.metadata.annotations.version}{\" \"}{@.metadata.annotations.service}{\"\\n\"}{end}").Output()
	if err != nil {
		panic(err)
	}
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

func getYamlConfigVersion(content, kind, name string) (string, string) {
	configs := RegSplit(content, "(?m:^[-]{3,})")
	for _, c := range configs {
		y, n, k := parseConfig(c)
		if n == name && k == kind {
			if annos, ok := y["annotations"].(map[interface{}]interface{}); ok {
				version, _ := annos["version"].(string)
				service, _ := annos["service"].(string)
				return version, service
			}
		}
	}
	return "", ""
}

func deploy(c *cli.Context) error {
	service := parseService()
	deploy := compile(readDeployYaml(), strconv.Itoa(service.Version), service.Name, service.commit)
	if err := ioutil.WriteFile("deploy-lock.yaml", []byte(deploy), 0644); err != nil {
		panic(err)
	}
	if !execute("/bin/sh", "kubectl apply -f deploy-lock.yaml") {
		return errors.New("failed")
	}
	return nil
}

func compile(src, version, name, commit string) string {
	return stringf.Format(src, map[string]string{
		"version": version,
		"name":    name,
		"commit":  commit,
	})
}

func inc(c *cli.Context) error {
	service := parseService()
	service.Version++
	saveService(service)
	return nil
}

func saveService(s Service) {
	data, err := yaml.Marshal(&s)
	if err != nil {
		panic(err)
	}

	if err := ioutil.WriteFile("service.yaml", data, 0644); err != nil {
		panic(err)
	}
}

func up(c *cli.Context) error {
	service := parseService()
	upstr := compile(service.Up, strconv.Itoa(service.Version), service.Name, service.commit)
	if !execute("/bin/sh", upstr) {
		return errors.New("failed")
	}
	return nil
}

func build() bool {
	service := parseService()
	buildstr := compile(service.Build, strconv.Itoa(service.Version), service.Name, service.commit)
	if !execute("/bin/sh", buildstr) {
		return false
	}
	saveService(service)
	return true
}

func parseService() Service {
	data, err := ioutil.ReadFile("service.yaml")
	if err != nil {
		panic(err)
	}
	s := Service{}
	if err := yaml.Unmarshal(data, &s); err != nil {
		panic(err)
	}
	s.commit = getGitCommit()
	return s
}

func getGitCommit() string {
	// check in git (HEAD)
	cmdArgs := []string{"-c" , "[ -f .git/HEAD ] && cat .git/$(cat .git/HEAD | cut -d ' ' -f 2)"}

	if cmdOut, err := exec.Command("/bin/sh", cmdArgs...).Output(); err == nil {
		sha := string(cmdOut)
		return sha[:7]
	}

	// check in env $GIT_COMMIT
	if c := os.Getenv("GIT_COMMIT"); c != "" {
		return c[:7]
	}

	if c := os.Getenv("BITBUCKET_COMMIT"); c != "" {
		return c[:7]
	}

	if c := os.Getenv("DRONE_COMMIT_SHA"); c != "" {

		return c[:7]
	}

	return "0000000"
}

// exec a shell script
func execute(shell, script string) (ok bool) {
	tmpfile, err := ioutil.TempFile("", "script")
	if err != nil {
		panic(err)
	}
	defer os.Remove(tmpfile.Name()) // clean up

	if _, err := tmpfile.Write([]byte(script)); err != nil {
		panic(err)
	}
	if err := tmpfile.Close(); err != nil {
		panic(err)
	}

	cmd := exec.Command(shell, "-e", tmpfile.Name())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		panic(err)
	}
	cmd.Start()
	chunk := make([]byte, 10)
	for {
		zero(chunk)
		if _, err := stdout.Read(chunk); err != nil {
			break
		}
		fmt.Print(string(chunk))
	}
	ok = true
	for {
		zero(chunk)
		if _, err := stderr.Read(chunk); err != nil {
			break
		}
		fmt.Print(string(chunk))
		ok = false
	}
	return ok
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func info(c *cli.Context) {
	service := parseService()
	key := c.Args().Get(0)
	switch key {
	case "name", "n":
		fmt.Print(service.Name)
	case "version", "v":
		fmt.Print(service.Version)
	case "commit", "c":
		fmt.Print(service.commit)
	}
}

func run(c *cli.Context) error {
	service := parseService()
	name := c.Args().Get(0)
	for n, c := range service.Run {
		println(fmt.Sprintf("%v", n), name)
		if name == fmt.Sprintf("%v", n) {
			rc, _ := c.(string)
			c := compile(rc, strconv.Itoa(service.Version), service.Name, service.commit)
			if !execute("/bin/sh", c) {
				return errors.New("failed")
			}
			return nil
		}
	}
	return errors.New("command not found")
}
