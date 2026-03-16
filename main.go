package main

import (
	"flag"
	"log"
	"os"

	licensecollector "github.com/aviadl/thirdPartyLicenseCollector/license-collector"
)

func main() {
	tmpGoDir := flag.String("go-project", "", "project directory")
	tmpNpmDir := flag.String("npm-project", "", "npm directory")
	// For some project - the node modules are not in the same directory as the package.json
	tmpNodeModulesDir := flag.String("npm-node-modules", "", "node_modules directory (optional, leave empty if it is in the same as npm-project)")
	out := flag.String("out", licensecollector.LicenseFileName, "output file")
	format := flag.String("format", licensecollector.DefaultLicenseFileFormat, "output format: text vs json")
	goMode := flag.String("go-mode", "vendor", "Go dependency resolution mode: 'vendor' reads vendor/modules.txt, 'list' uses 'go list -m' and the module cache")
	flag.Parse()
	log.SetFlags(0)

	err := licensecollector.Collect(*tmpGoDir, *tmpNpmDir, *tmpNodeModulesDir, *out, *format, *goMode)
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
