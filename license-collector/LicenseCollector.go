package licensecollector

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ryanuber/go-license"
)

// LicenseFileName is the default created license file name
const LicenseFileName = "THIRD_PARTY_LICENSE"
const DefaultLicenseFileFormat = "txt"
const vendorGoModuleFile = "modules.txt"

// GoMode controls how Go dependencies are resolved.
// "vendor" reads vendor/modules.txt (original behavior).
// "list" uses "go list -m -json all" and reads from the module cache.
const (
	GoModeVendor = "vendor"
	GoModeList   = "list"
)

// licenseMissing indicates that a license is missing
var licenseMissing = false

// Collect collects licenses from npm and or go projects.
// goMode controls Go dependency resolution: "vendor" or "list".
func Collect(projectGO, projectNPM string, projectNodeModules string, fileName string, fileFormat string, goMode string) error {
	licenseMap := map[string][]string{}
	foundManualLicense := map[string]string{}

	licenseMissing = false
	var err error
	if len(projectGO) > 0 {
		switch goMode {
		case GoModeVendor:
			err = collectGoLicenseFiles(projectGO, licenseMap, foundManualLicense)
		case GoModeList:
			err = collectGoLicenseFilesFromList(projectGO, licenseMap, foundManualLicense)
		default:
			return fmt.Errorf("invalid -go-mode %q: must be %q or %q", goMode, GoModeVendor, GoModeList)
		}
	}
	if len(projectNPM) > 0 {
		err = collectNpmLicenseFiles(projectNPM, projectNodeModules, licenseMap, foundManualLicense)
	}
	if err != nil {
		return err
	}
	if len(licenseMap)+len(foundManualLicense) == 0 {
		return errors.New("no licenses handled")
	}
	if licenseMissing {
		return errors.New("license missing")
	}
	fileData, err := generateLicenseFile(licenseMap, foundManualLicense, fileFormat)
	if err != nil {
		return err
	}
	err = os.WriteFile(fileName, fileData, 0644)
	if err != nil {
		return err
	}
	log.Printf("generated license with name %s\n", fileName)
	return nil
}

func collectGoLicenseFiles(tmpGoDir string, licenseMap map[string][]string, foundManualLicense map[string]string) error {
	dir := filepath.Join(tmpGoDir, "vendor")
	log.Println("Go Project dir: ", dir)
	// test go modules
	fileName := filepath.Join(dir, vendorGoModuleFile)
	log.Println("Processing go module file: ", fileName)
	fileHandle, err := os.Open(fileName)
	if err != nil {
		log.Println(err)
		log.Printf("failed finding %s for third party packages. make sure you 'go mod vendor'\n", vendorGoModuleFile)
		return err
	}
	defer func() { _ = fileHandle.Close() }()

	packageMap := make(map[string]struct{})
	fileScanner := bufio.NewScanner(fileHandle)
	for fileScanner.Scan() {
		line := strings.TrimSpace(fileScanner.Text())
		// take all packages.
		if strings.HasPrefix(line, "##") { // skip "## explicit" line which was added to modules.txt in GO 1.14
			continue
		}
		if strings.Index(line, "#") != 0 {
			continue
		}
		linePackage := strings.SplitN(line, " ", 3)[1]
		if len(linePackage) > 0 {
			packageMap[linePackage] = struct{}{}
		}
	}

	manualLicense, err := prepareManualLicense(tmpGoDir)
	if err != nil {
		return err
	}
	for packagePath := range packageMap {
		doParseFile(dir, packagePath, manualLicense, licenseMap, foundManualLicense)
	}
	return nil
}

// goListModule represents the JSON output of "go list -m -json".
type goListModule struct {
	Path string `json:"Path"`
	Dir  string `json:"Dir"`
	Main bool   `json:"Main"`
}

// collectGoLicenseFilesFromList uses "go list -m -json all" to enumerate
// dependencies and reads license files from the module cache directory.
// This avoids the need for a vendor directory.
func collectGoLicenseFilesFromList(tmpGoDir string, licenseMap map[string][]string, foundManualLicense map[string]string) error {
	log.Println("Go Project dir (list mode): ", tmpGoDir)

	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = tmpGoDir
	out, err := cmd.Output()
	if err != nil {
		log.Println(err)
		log.Println("failed running 'go list -m -json all'. make sure Go modules are available (run 'go mod download')")
		return err
	}

	// "go list -m -json all" outputs concatenated JSON objects (not an array).
	// Decode them one by one.
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	var modules []goListModule
	for decoder.More() {
		var mod goListModule
		if err := decoder.Decode(&mod); err != nil {
			return fmt.Errorf("failed to parse go list output: %w", err)
		}
		// Skip the main module(s).
		if mod.Main {
			continue
		}
		// Module not in cache — log so missing licenses are visible.
		if mod.Dir == "" {
			log.Printf("skipping %s: not in module cache (run 'go mod download')\n", mod.Path)
			continue
		}
		modules = append(modules, mod)
	}

	if len(modules) == 0 {
		return errors.New("no modules found via 'go list -m -json all'")
	}

	log.Printf("Found %d dependency modules\n", len(modules))

	manualLicense, err := prepareManualLicense(tmpGoDir)
	if err != nil {
		return err
	}

	for _, mod := range modules {
		// Reuse the shared processModule helper which handles manual
		// license lookup, auto-detection, and result recording — same
		// logic as doParseFile but accepting an explicit module name
		// and root directory instead of deriving them from a vendor tree.
		processModule(mod.Path, mod.Dir, manualLicense, licenseMap, foundManualLicense)
	}
	return nil
}

func collectNpmLicenseFiles(tmpNpmDir string, tmpNodeModulesDir string, licenseMap map[string][]string, foundManualLicense map[string]string) error {
	log.Println("NPM Project dir: ", tmpNpmDir)
	nodeModulesDir := tmpNpmDir
	if len(tmpNodeModulesDir) > 0 {
		nodeModulesDir = tmpNodeModulesDir
	}
	dir := filepath.Join(nodeModulesDir, "node_modules")
	fileName := filepath.Join(tmpNpmDir, "package.json")
	log.Println("Processing package file: ", fileName)
	data, err := os.ReadFile(fileName)
	if err != nil {
		log.Println(err)
		log.Println("Failed processing npm licenses")
		return err
	}

	packageMap := map[string]interface{}{}
	_ = json.Unmarshal(data, &packageMap)
	//Get the package list
	rawPackages, ok := packageMap["dependencies"]
	var packages map[string]interface{}
	if ok {
		packages = rawPackages.(map[string]interface{})
	}

	manualLicense, err := prepareManualLicense(tmpNpmDir)
	if err != nil {
		return err
	}
	for fileDir := range packages {
		doParseFile(dir, fileDir, manualLicense, licenseMap, foundManualLicense)
	}
	return nil
}

// processModule handles manual license lookup, auto-detection, and result
// recording for a single module. Both vendor and list modes call this.
//   - moduleName: the Go import path (e.g. "github.com/foo/bar")
//   - moduleDir:  the on-disk directory containing the module source
func processModule(moduleName, moduleDir string, manualLicense map[string]string, licenseMap map[string][]string, foundManualLicense map[string]string) {
	lDir, licenseDescriptor, missing := parseLicenseManual(moduleName, manualLicense)
	if missing {
		l, err := license.NewFromDir(moduleDir)
		if err != nil {
			log.Println("Could not find license for ", moduleName)
			licenseMissing = true
			return
		}
		if l.Type != "" {
			arr := licenseMap[l.Type]
			if !InStringSlice(arr, moduleName) {
				arr = append(arr, moduleName)
				licenseMap[l.Type] = arr
			}
		}
	} else if len(licenseDescriptor) > 0 {
		if licenseDescriptor == "ignore" {
			return
		}
		if strings.Index(licenseDescriptor, " ") == -1 {
			arr, exists := licenseMap[licenseDescriptor]
			if exists {
				if !InStringSlice(arr, lDir) {
					arr = append(arr, lDir)
					licenseMap[licenseDescriptor] = arr
				}
			} else {
				foundManualLicense[lDir] = licenseDescriptor
			}
		} else {
			foundManualLicense[lDir] = licenseDescriptor
		}
	}
}

// doParseFile resolves a module from a vendor-style directory tree and
// delegates to processModule. Kept for vendor mode where the module root
// must be found by walking parent directories.
func doParseFile(dir, fileDir string, manualLicense map[string]string, licenseMap map[string][]string, foundManualLicense map[string]string) {
	// First check manual license by package path.
	_, _, manualMissing := parseLicenseManual(fileDir, manualLicense)
	if manualMissing {
		// Auto-detect: walk parent dirs to find the license root.
		lDir, _, _ := parseLicenseAuto(dir, fileDir)
		moduleName := lDir[len(dir)+1:]
		processModule(moduleName, lDir, manualLicense, licenseMap, foundManualLicense)
	} else {
		// Manual license found — delegate with the vendor dir as module root.
		processModule(fileDir, filepath.Join(dir, fileDir), manualLicense, licenseMap, foundManualLicense)
	}
}

func generateLicenseFile(lTypeMap map[string][]string, lContentMap map[string]string, format string) ([]byte, error) {
	licenseMap := initLicenseMap()
	res := ""
	jsonRes := map[string]string{}
	wrongLicense := map[string][]string{}
	for k, v := range lTypeMap {
		fullLicense, ok := licenseMap[k]
		if !ok {
			wrongLicense[k] = v
			continue
		}
		projects := ""
		for _, p := range v {
			projects += p + "\n"
			jsonRes[p] = fullLicense
		}
		projects += fullLicense + "\n"
		res += projects
	}
	if len(wrongLicense) > 0 {
		errMsg := "Wrong license files for the following libs"
		for k, v := range wrongLicense {
			errMsg += "\n " + k + ": " + fmt.Sprintf("%v", v)
		}
		var errRes []byte
		if format == "json" {
			errRes = []byte("{}")
		} else {
			errRes = []byte("")
		}
		return errRes, fmt.Errorf(errMsg)
	}
	for project, fullLicense := range lContentMap {
		res += project + "\n" + fullLicense + "\n"
		jsonRes[project] = fullLicense
	}
	var bRes []byte
	if format == "json" {
		var err error
		bRes, err = json.Marshal(jsonRes)
		if err != nil {
			return []byte("{}"), err
		}
	} else {
		bRes = []byte(res)
	}
	return bRes, nil
}

// InStringSlice checks if val string is in s slice, case insensitive.
func InStringSlice(slice []string, val string) bool {
	for _, v := range slice {
		if strings.EqualFold(v, val) {
			return true
		}
	}
	return false
}

func parseLicenseAuto(dir, fileDir string) (lDir string, lType string, missing bool) {
	// This case will work if there is a guessable license file in the
	// current working directory.
	dirs := strings.Split(fileDir, "/")
	currentDir := dir
	missing = true
	lDir = filepath.Join(dir, fileDir)
	for i := range dirs {
		currentDir = filepath.Join(currentDir, dirs[i])
		l, err := license.NewFromDir(currentDir)
		if err != nil {
			continue
		}
		missing = false
		lType = l.Type
		lDir = currentDir
		break
	}
	return
}

func prepareManualLicense(vendorDir string) (map[string]string, error) {
	fileName := filepath.Join(vendorDir, "manualLicense.json")
	log.Println("Processing manual license file: ", fileName)
	data, err := os.ReadFile(fileName)
	if err != nil {
		log.Println("No manual license file")
		return map[string]string{}, nil
	}
	licenseMap := map[string]string{}
	err = json.Unmarshal(data, &licenseMap)
	if err != nil {
		log.Printf("Failed parsing license file with error [%s]\n", err)
	}
	return licenseMap, err
}

// parseLicenseManual will look for the manual license file index, to add files that cannot be found automatically
func parseLicenseManual(dir string, manualFileMap map[string]string) (lDir string, lContent string, missing bool) {
	dirs := strings.Split(dir, "/")
	currentDir := ""
	missing = true
	lDir = dir
	for i := range dirs {
		currentDir = filepath.Join(currentDir, dirs[i])
		content, exists := manualFileMap[currentDir]
		if exists {
			missing = false
			lContent = content
			lDir = currentDir
			break
		}
	}
	return
}
