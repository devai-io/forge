// Package seed embeds the optional demo data (FORGE_DEMO_DATA=true): three
// fictional projects with repos, servers, endpoints and a backlog, loaded
// once into an empty database so a fresh install has something to click
// through. Delete the projects when you add your own.
package seed

import _ "embed"

//go:embed demo.json
var Demo []byte
