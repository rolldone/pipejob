package main

import (
	"bufio"
	"os"
	"strings"
)

func parseEnvFile(path string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// strip surrounding quotes if present
		if strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"") {
			val = strings.Trim(val, "\"")
		}
		out[key] = val
	}
	if scanner.Err() != nil {
		return nil, scanner.Err()
	}
	return out, nil
}

func interpolate(tmpl string, vars map[string]string) string {
	if tmpl == "" {
		return tmpl
	}
	res := tmpl
	for k, v := range vars {
		// support {{KEY}} and {{ KEY }} and {{.KEY}}
		res = strings.ReplaceAll(res, "{{"+k+"}}", v)
		res = strings.ReplaceAll(res, "{{ "+k+" }}", v)
		res = strings.ReplaceAll(res, "{{."+k+"}}", v)
		res = strings.ReplaceAll(res, "{{ ."+k+" }}", v)
	}
	return res
}
