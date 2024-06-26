/*
Copyright 2024 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Forked from: https://github.com/drone/envsubst
// MIT License
// Copyright (c) 2017 drone.io.

package parse

import (
	"errors"
)

var (
	// ErrBadSubstitution represents a substitution parsing error.
	ErrBadSubstitution = errors.New("bad substitution")

	// ErrMissingClosingBrace represents a missing closing brace "}" error.
	ErrMissingClosingBrace = errors.New("missing closing brace")

	// ErrParseVariableName represents the error when unable to parse a
	// variable name within a substitution.
	ErrParseVariableName = errors.New("unable to parse variable name")

	// ErrParseFuncSubstitution represents the error when unable to parse the
	// substitution within a function parameter.
	ErrParseFuncSubstitution = errors.New("unable to parse substitution within function")

	// ErrParseDefaultFunction represent the error when unable to parse a
	// default function.
	ErrParseDefaultFunction = errors.New("unable to parse default function")
)

// Tree is the representation of a single parsed SQL statement.
type Tree struct {
	Root Node

	// Parsing only; cleared after parse.
	scanner *scanner
}

// Parse parses the string and returns a Tree.
func Parse(buf string) (*Tree, error) {
	t := new(Tree)
	t.scanner = new(scanner)
	return t.Parse(buf)
}

// Parse parses the string buffer to construct an ast
// representation for expansion.
func (t *Tree) Parse(buf string) (tree *Tree, err error) {
	t.scanner.init(buf)
	t.Root, err = t.parseAny()
	return t, err
}

func (t *Tree) parseAny() (Node, error) {
	t.scanner.accept = acceptRune
	t.scanner.mode = scanIdent | scanLbrack | scanEscape
	t.scanner.escapeChars = dollar

	switch t.scanner.scan() {
	case tokenIdent:
		left := newTextNode(
			t.scanner.string(),
		)
		right, err := t.parseAny()
		switch {
		case err != nil:
			return nil, err
		case right == empty:
			return left, nil
		}
		return newListNode(left, right), nil
	case tokenEOF:
		return empty, nil
	case tokenLbrack:
		left, err := t.parseFunc()
		if err != nil {
			return nil, err
		}

		right, err := t.parseAny()
		switch {
		case err != nil:
			return nil, err
		case right == empty:
			return left, nil
		}
		return newListNode(left, right), nil
	}

	return nil, ErrBadSubstitution
}

func (t *Tree) parseFunc() (Node, error) {
	// Turn on all escape characters
	t.scanner.escapeChars = escapeAll
	switch t.scanner.peek() {
	case '#':
		return t.parseLenFunc()
	}

	var name string
	t.scanner.accept = acceptIdent
	t.scanner.mode = scanIdent

	switch t.scanner.scan() {
	case tokenIdent:
		name = t.scanner.string()
	default:
		return nil, ErrParseVariableName
	}

	switch t.scanner.peek() {
	case ':':
		return t.parseDefaultOrSubstr(name)
	case '=':
		return t.parseDefaultFunc(name)
	case ',', '^':
		return t.parseCasingFunc(name)
	case '/':
		return t.parseReplaceFunc(name)
	case '#':
		return t.parseRemoveFunc(name, acceptHashFunc)
	case '%':
		return t.parseRemoveFunc(name, acceptPercentFunc)
	}

	t.scanner.accept = acceptIdent
	t.scanner.mode = scanRbrack
	switch t.scanner.scan() {
	case tokenRbrack:
		return newFuncNode(name), nil
	default:
		return nil, ErrMissingClosingBrace
	}
}

// parse a substitution function parameter.
func (t *Tree) parseParam(accept acceptFunc, mode byte) (Node, error) {
	t.scanner.accept = accept
	t.scanner.mode = mode | scanLbrack
	switch t.scanner.scan() {
	case tokenLbrack:
		return t.parseFunc()
	case tokenIdent:
		return newTextNode(
			t.scanner.string(),
		), nil
	case tokenRbrack:
		return newTextNode(
			t.scanner.string(),
		), nil
	default:
		return nil, ErrParseFuncSubstitution
	}
}

// parse either a default or substring substitution function.
func (t *Tree) parseDefaultOrSubstr(name string) (Node, error) {
	t.scanner.read()
	r := t.scanner.peek()
	t.scanner.unread()
	switch r {
	case '=', '-', '?', '+':
		return t.parseDefaultFunc(name)
	default:
		return t.parseSubstrFunc(name)
	}
}

// parses the ${param:offset} string function
// parses the ${param:offset:length} string function
func (t *Tree) parseSubstrFunc(name string) (Node, error) {
	node := new(FuncNode)
	node.Param = name

	t.scanner.accept = acceptOneColon
	t.scanner.mode = scanIdent
	switch t.scanner.scan() {
	case tokenIdent:
		node.Name = t.scanner.string()
	default:
		return nil, ErrBadSubstitution
	}

	// scan arg[1]
	{
		param, err := t.parseParam(rejectColonClose, scanIdent)
		if err != nil {
			return nil, err
		}

		// param.Value = t.scanner.string()
		node.Args = append(node.Args, param)
	}

	// expect delimiter or close
	t.scanner.accept = acceptColon
	t.scanner.mode = scanIdent | scanRbrack
	switch t.scanner.scan() {
	case tokenRbrack:
		return node, nil
	case tokenIdent:
		// no-op
	default:
		return nil, ErrBadSubstitution
	}

	// scan arg[2]
	{
		param, err := t.parseParam(acceptNotClosing, scanIdent)
		if err != nil {
			return nil, err
		}
		node.Args = append(node.Args, param)
	}

	return node, t.consumeRbrack()
}

// parses the ${param%word} string function
// parses the ${param%%word} string function
// parses the ${param#word} string function
// parses the ${param##word} string function
func (t *Tree) parseRemoveFunc(name string, accept acceptFunc) (Node, error) {
	node := new(FuncNode)
	node.Param = name

	t.scanner.accept = accept
	t.scanner.mode = scanIdent
	switch t.scanner.scan() {
	case tokenIdent:
		node.Name = t.scanner.string()
	default:
		return nil, ErrBadSubstitution
	}

	// scan arg[1]
	{
		param, err := t.parseParam(acceptNotClosing, scanIdent)
		if err != nil {
			return nil, err
		}

		// param.Value = t.scanner.string()
		node.Args = append(node.Args, param)
	}

	return node, t.consumeRbrack()
}

// parses the ${param/pattern/string} string function
// parses the ${param//pattern/string} string function
// parses the ${param/#pattern/string} string function
// parses the ${param/%pattern/string} string function
func (t *Tree) parseReplaceFunc(name string) (Node, error) {
	node := new(FuncNode)
	node.Param = name

	t.scanner.accept = acceptReplaceFunc
	t.scanner.mode = scanIdent
	switch t.scanner.scan() {
	case tokenIdent:
		node.Name = t.scanner.string()
	default:
		return nil, ErrBadSubstitution
	}

	// scan arg[1]
	{
		param, err := t.parseParam(acceptNotSlash, scanIdent|scanEscape)
		if err != nil {
			return nil, err
		}
		node.Args = append(node.Args, param)
	}

	// expect delimiter
	t.scanner.accept = acceptSlash
	t.scanner.mode = scanIdent
	switch t.scanner.scan() {
	case tokenIdent:
		// no-op
	default:
		return nil, ErrBadSubstitution
	}

	// check for blank string
	switch t.scanner.peek() {
	case '}':
		return node, t.consumeRbrack()
	}

	// scan arg[2]
	{
		param, err := t.parseParam(acceptNotClosing, scanIdent|scanEscape)
		if err != nil {
			return nil, err
		}
		node.Args = append(node.Args, param)
	}

	return node, t.consumeRbrack()
}

// parses the ${parameter=word} string function
// parses the ${parameter:=word} string function
// parses the ${parameter:-word} string function
// parses the ${parameter:?word} string function
// parses the ${parameter:+word} string function
func (t *Tree) parseDefaultFunc(name string) (Node, error) {
	node := new(FuncNode)
	node.Param = name

	t.scanner.accept = acceptDefaultFunc
	if t.scanner.peek() == '=' {
		t.scanner.accept = acceptOneEqual
	}
	t.scanner.mode = scanIdent
	switch t.scanner.scan() {
	case tokenIdent:
		node.Name = t.scanner.string()
	default:
		return nil, ErrParseDefaultFunction
	}

	// loop through all possible runes in default param
	for {
		// this acts as the break condition. Peek to see if we reached the end
		switch t.scanner.peek() {
		case '}':
			return node, t.consumeRbrack()
		}
		param, err := t.parseParam(acceptNotClosing, scanIdent)
		if err != nil {
			return nil, err
		}

		node.Args = append(node.Args, param)
	}
}

// parses the ${param,} string function
// parses the ${param,,} string function
// parses the ${param^} string function
// parses the ${param^^} string function
func (t *Tree) parseCasingFunc(name string) (Node, error) {
	node := new(FuncNode)
	node.Param = name

	t.scanner.accept = acceptCasingFunc
	t.scanner.mode = scanIdent
	switch t.scanner.scan() {
	case tokenIdent:
		node.Name = t.scanner.string()
	default:
		return nil, ErrBadSubstitution
	}

	return node, t.consumeRbrack()
}

// parses the ${#param} string function
func (t *Tree) parseLenFunc() (Node, error) {
	node := new(FuncNode)

	t.scanner.accept = acceptOneHash
	t.scanner.mode = scanIdent
	switch t.scanner.scan() {
	case tokenIdent:
		node.Name = t.scanner.string()
	default:
		return nil, ErrBadSubstitution
	}

	t.scanner.accept = acceptIdent
	t.scanner.mode = scanIdent
	switch t.scanner.scan() {
	case tokenIdent:
		node.Param = t.scanner.string()
	default:
		return nil, ErrBadSubstitution
	}

	return node, t.consumeRbrack()
}

// consumeRbrack consumes a right closing bracket. If a closing
// bracket token is not consumed an ErrBadSubstitution is returned.
func (t *Tree) consumeRbrack() error {
	t.scanner.mode = scanRbrack
	if t.scanner.scan() != tokenRbrack {
		return ErrBadSubstitution
	}
	return nil
}

// consumeDelimiter consumes a function argument delimiter. If a
// delimiter is not consumed an ErrBadSubstitution is returned.
// func (t *Tree) consumeDelimiter(accept acceptFunc, mode uint) error {
// 	t.scanner.accept = accept
// 	t.scanner.mode = mode
// 	if t.scanner.scan() != tokenRbrack {
// 		return ErrBadSubstitution
// 	}
// 	return nil
// }
