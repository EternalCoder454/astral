package app

// version is the single source of truth for the app's version: the Makefile
// reads it out of this file, so a plain `go build .` and a `make install` can
// never report different numbers at each other.
var version = "0.3.0"

// projectURL is where Astral lives. Used by the about dialog for the website
// and issue links.
const projectURL = "https://github.com/EternalCoder454/astral"
