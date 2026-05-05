package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const versionInfoPath = "cmd/nvrclip/versioninfo.json"

type versionInfo struct {
	FixedFileInfo  fixedFileInfo  `json:"FixedFileInfo"`
	StringFileInfo stringFileInfo `json:"StringFileInfo"`
	VarFileInfo    map[string]any `json:"VarFileInfo"`
}

type fixedFileInfo struct {
	FileVersion    fileVersion `json:"FileVersion"`
	ProductVersion fileVersion `json:"ProductVersion"`
	FileFlagsMask  string      `json:"FileFlagsMask"`
	FileFlags      string      `json:"FileFlags "`
	FileOS         string      `json:"FileOS"`
	FileType       string      `json:"FileType"`
	FileSubType    string      `json:"FileSubType"`
}

type fileVersion struct {
	Major int `json:"Major"`
	Minor int `json:"Minor"`
	Patch int `json:"Patch"`
	Build int `json:"Build"`
}

type stringFileInfo struct {
	Comments         string `json:"Comments"`
	CompanyName      string `json:"CompanyName"`
	FileDescription  string `json:"FileDescription"`
	FileVersion      string `json:"FileVersion"`
	InternalName     string `json:"InternalName"`
	LegalCopyright   string `json:"LegalCopyright"`
	LegalTrademarks  string `json:"LegalTrademarks"`
	OriginalFilename string `json:"OriginalFilename"`
	PrivateBuild     string `json:"PrivateBuild"`
	ProductName      string `json:"ProductName"`
	ProductVersion   string `json:"ProductVersion"`
	SpecialBuild     string `json:"SpecialBuild"`
}

var versionRe = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:\.([0-9]+))?$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var versionRaw string
	var out string
	flag.StringVar(&versionRaw, "version", "", "version to embed, e.g. 0.2.0 or 0.2.0.15")
	flag.StringVar(&out, "out", "nvrclip.exe", "output binary path")
	flag.Parse()
	if versionRaw == "" && flag.NArg() > 0 {
		versionRaw = flag.Arg(0)
	}
	if versionRaw == "" {
		return errors.New("missing --version, e.g. go run ./tools/build --version 0.2.0")
	}

	versionString, fixed, err := parseVersion(versionRaw)
	if err != nil {
		return err
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	if err := updateVersionInfo(filepath.Join(root, versionInfoPath), versionString, fixed); err != nil {
		return err
	}
	if err := runCmd(root, "go", "generate", "./cmd/nvrclip"); err != nil {
		return err
	}
	if err := runCmd(root, "go", "build", "-o", out, "./cmd/nvrclip"); err != nil {
		return err
	}
	fmt.Printf("built %s with version %s\n", out, versionString)
	return nil
}

func parseVersion(raw string) (string, fileVersion, error) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return "", fileVersion{}, fmt.Errorf("invalid version %q; expected 0.2.0 or 0.2.0.15", raw)
	}
	nums := [4]int{}
	for i := 1; i <= 4; i++ {
		if m[i] == "" {
			continue
		}
		n, err := strconv.Atoi(m[i])
		if err != nil {
			return "", fileVersion{}, err
		}
		nums[i-1] = n
	}
	versionString := fmt.Sprintf("%d.%d.%d", nums[0], nums[1], nums[2])
	if m[4] != "" {
		versionString = fmt.Sprintf("%s.%d", versionString, nums[3])
	}
	return versionString, fileVersion{Major: nums[0], Minor: nums[1], Patch: nums[2], Build: nums[3]}, nil
}

func updateVersionInfo(path string, versionString string, fixed fileVersion) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var info versionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return err
	}
	info.FixedFileInfo.FileVersion = fixed
	info.FixedFileInfo.ProductVersion = fixed
	info.StringFileInfo.FileVersion = versionString
	info.StringFileInfo.ProductVersion = versionString
	out, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o644)
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			return "", errors.New("could not find go.mod")
		}
		dir = next
	}
}

func runCmd(dir string, name string, args ...string) error {
	fmt.Printf("> %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
