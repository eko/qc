//go:build !unix

package main

// inForeground reports true: process groups are a Unix notion.
func inForeground(
	int,
) bool {
	return true
}
