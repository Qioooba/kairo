//go:build !windows

package main

func openFloatingPetWindow(url string) {
	openBrowser(url)
}
