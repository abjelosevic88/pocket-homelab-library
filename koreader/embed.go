// Package koreader holds the Pocket Library plugin for KOReader, served by
// the app as /koreader/plugin.zip.
package koreader

import "embed"

// Plugin is the plugin folder.
//
//go:embed all:pocketlibrary.koplugin
var Plugin embed.FS

// Dir is the plugin's folder name inside Plugin (and inside the zip).
const Dir = "pocketlibrary.koplugin"
