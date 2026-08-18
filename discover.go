package main

import "fmt"

func runDiscover() error {
	if fileExists("Chart.yaml") || dirExists("secrets") {
		fmt.Println("match")
	}
	return nil
}
