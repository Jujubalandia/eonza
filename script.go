// Copyright 2020 Alexey Krivonogov. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"eonza/lib"
	"fmt"
	"path"
	"strings"

	"github.com/kataras/golog"
	"gopkg.in/yaml.v2"
)

var (
	scripts map[string]*Script
)

type ParamType int

const (
	PCheckbox ParamType = iota
	PTextarea
)

type scriptSettings struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Desc  string `json:"desc,omitempty"`
	Unrun bool   `json:"unrun,omitempty"`
}

type scriptOptions struct {
	Initial  string `yaml:"initial,omitempty"`
	Default  string `yaml:"default,omitempty"`
	Required bool   `yaml:"required,omitempty"`
}

type scriptParam struct {
	Name    string    `json:"name"`
	Title   string    `json:"title"`
	Type    ParamType `json:"type"`
	Options string    `json:"options,omitempty"`

	options scriptOptions
}

type scriptTree struct {
	Name     string                 `json:"name"`
	Open     bool                   `json:"open,omitempty"`
	Disable  bool                   `json:"disable,omitempty"`
	Values   map[string]interface{} `json:"values,omitempty"`
	Children []scriptTree           `json:"children,omitempty"`
}

type Script struct {
	Settings scriptSettings `json:"settings"`
	Params   []scriptParam  `json:"params,omitempty"`
	Tree     []scriptTree   `json:"tree,omitempty"`
	Code     string         `json:"code,omitempty"`
	folder   bool           // can have other commands inside
	embedded bool           // Embedded script
	initial  string         // Initial value
}

func getScript(name string) (script *Script) {
	return scripts[lib.IdName(name)]
}

func setScript(name string, script *Script) error {
	var ivalues map[string]string

	scripts[lib.IdName(name)] = script
	if len(script.Params) > 0 {
		ivalues = make(map[string]string)
	}
	for i, par := range script.Params {
		if len(par.Options) > 0 {
			var options scriptOptions
			if err := yaml.Unmarshal([]byte(par.Options), &options); err != nil {
				return err
			}
			script.Params[i].options = options
			if len(options.Initial) > 0 {
				ivalues[par.Name] = options.Initial
			}
		}
	}
	if len(ivalues) > 0 {
		initial, err := json.Marshal(ivalues)
		if err != nil {
			return err
		}
		script.initial = string(initial)
	}
	return nil
}

func delScript(name string) {
	name = lib.IdName(name)
	delete(scripts, name)
	delete(storage.Scripts, name)
}

func InitScripts() {
	scripts = make(map[string]*Script)
	isfolder := func(script *Script) bool {
		return script.Settings.Name == SourceCode ||
			strings.Contains(script.Code, `%body%`)
	}
	for _, tpl := range _escDirs["../eonza-assets/scripts"] {
		var script Script
		fname := tpl.Name()
		data := FileAsset(path.Join(`scripts`, fname))
		if err := yaml.Unmarshal(data, &script); err != nil {
			golog.Fatal(err)
		}
		script.embedded = true
		script.folder = isfolder(&script)
		if err := setScript(script.Settings.Name, &script); err != nil {
			golog.Fatal(err)
		}
	}
	for name, item := range storage.Scripts {
		// TODO: this is a temporary fix
		if strings.Contains(name, `-`) {
			continue
		}
		//
		item.folder = isfolder(item)
		if err := setScript(name, item); err != nil {
			golog.Fatal(err)
		}
	}
}

func (script *Script) Validate() error {
	if !lib.ValidateSysName(script.Settings.Name) {
		return fmt.Errorf(Lang(`invalidfield`), Lang(`name`))
	}
	if len(script.Settings.Title) == 0 {
		return fmt.Errorf(Lang(`invalidfield`), Lang(`title`))
	}
	return nil
}

func ScriptDependences(name string) []ScriptItem {
	var ret []ScriptItem

	// TODO: enumerate all commands
	return ret
}

func (script *Script) SaveScript(original string) error {
	if script.embedded {
		// TODO: error
	}
	if len(original) > 0 && original != script.Settings.Name {
		if getScript(script.Settings.Name) != nil {
			return fmt.Errorf(Lang(`errscriptname`), script.Settings.Name)
		}
		if deps := ScriptDependences(original); len(deps) > 0 {
			// TODO: error dependences
		}
		delScript(original)
	}
	script.folder = script.Settings.Name == SourceCode ||
		strings.Contains(script.Code, `%body%`)
	if err := setScript(script.Settings.Name, script); err != nil {
		return err
	}
	storage.Scripts[lib.IdName(script.Settings.Name)] = script
	return SaveStorage()
}

func DeleteScript(name string) error {
	script := getScript(name)
	if script == nil {
		return fmt.Errorf(Lang(`erropen`, name))
	}
	if script.embedded {
		// TODO: error
	}
	if deps := ScriptDependences(name); len(deps) > 0 {
		// TODO: error dependences
	}
	delScript(name)
	return SaveStorage()
}