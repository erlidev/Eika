// Package github searches GitHub's code, repositories, and issues, and holds
// the request headers every GitHub API call sends, which fetch shares for its
// GitHub reads. Code search needs a token; the other two work without one at
// GitHub's much lower anonymous rate.
package github
