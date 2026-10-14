package requestfilter

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

var errBodyLimitExceeded = errors.New("requestfilter: body parser limit exceeded")

func isSupportedBodyMediaType(mediaType string) bool {
	return mediaType == "application/x-www-form-urlencoded" || isJSONMediaType(mediaType) ||
		isXMLMediaType(mediaType) || mediaType == "multipart/form-data"
}

func isJSONMediaType(mediaType string) bool {
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func isXMLMediaType(mediaType string) bool {
	return mediaType == "application/xml" || mediaType == "text/xml" || strings.HasSuffix(mediaType, "+xml")
}

func parseBodyParameters(request normalizedRequest, mediaType string, options normalizedOptions) ([]Parameter, error) {
	parameters := make(map[string]Parameter)
	add := func(location ParameterLocation, name string) error {
		key := digestFields(string(location), name)
		if _, exists := parameters[key]; exists {
			return nil
		}
		if len(parameters) >= options.MaxParameters {
			return errBodyLimitExceeded
		}
		parameters[key] = Parameter{Location: location, Name: name}
		return nil
	}
	var err error
	switch {
	case mediaType == "application/x-www-form-urlencoded":
		err = parseFormParameters(request.request.Body, add)
	case isJSONMediaType(mediaType):
		err = addJSONParameters(request.request.Body, options.MaxJSONDepth, add)
	case isXMLMediaType(mediaType):
		err = addXMLParameters(request.request.Body, options.MaxXMLDepth, options.MaxXMLNodes, add)
	case mediaType == "multipart/form-data":
		err = addMultipartParameters(request.request.Body, request.contentType, options.MaxMultipartParts, add)
	default:
		return nil, io.ErrUnexpectedEOF
	}
	if err != nil {
		return nil, err
	}
	result := make([]Parameter, 0, len(parameters))
	for _, parameter := range parameters {
		result = append(result, parameter)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Location == result[right].Location {
			return result[left].Name < result[right].Name
		}
		return result[left].Location < result[right].Location
	})
	return result, nil
}

func parseFormParameters(body []byte, add func(ParameterLocation, string) error) error {
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return err
	}
	for name := range form {
		if err := add(ParameterForm, name); err != nil {
			return err
		}
	}
	return nil
}

func addJSONParameters(body []byte, maximumDepth int, add func(ParameterLocation, string) error) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return io.ErrUnexpectedEOF
	}
	return walkJSON(value, "", 0, maximumDepth, add)
}

func walkJSON(value any, path string, depth, maximumDepth int, add func(ParameterLocation, string) error) error {
	if depth > maximumDepth {
		return io.ErrUnexpectedEOF
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 {
			return add(ParameterJSON, jsonParameterName(path))
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			childPath := path + "/" + escapeJSONPointer(key)
			if err := walkJSON(typed[key], childPath, depth+1, maximumDepth, add); err != nil {
				return err
			}
		}
	case []any:
		if len(typed) == 0 {
			return add(ParameterJSON, jsonParameterName(path))
		}
		for index, item := range typed {
			childPath := path + "/" + strconv.Itoa(index)
			if err := walkJSON(item, childPath, depth+1, maximumDepth, add); err != nil {
				return err
			}
		}
	default:
		return add(ParameterJSON, jsonParameterName(path))
	}
	return nil
}

type xmlElement struct {
	name       xml.Name
	attributes []xml.Name
	children   []*xmlElement
	hasText    bool
}

func addXMLParameters(body []byte, maximumDepth, maximumNodes int, add func(ParameterLocation, string) error) error {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	stack := make([]*xmlElement, 0, 8)
	var root *xmlElement
	nodeCount := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if len(stack) >= maximumDepth {
				return errBodyLimitExceeded
			}
			nodeCount++
			if nodeCount > maximumNodes {
				return errBodyLimitExceeded
			}
			current := &xmlElement{name: typed.Name}
			for _, attribute := range typed.Attr {
				if attribute.Name.Local == "xmlns" || attribute.Name.Space == "xmlns" {
					continue
				}
				nodeCount++
				if nodeCount > maximumNodes {
					return errBodyLimitExceeded
				}
				current.attributes = append(current.attributes, attribute.Name)
			}
			if len(stack) == 0 {
				if root != nil {
					return io.ErrUnexpectedEOF
				}
				root = current
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, current)
			}
			stack = append(stack, current)
		case xml.CharData:
			if len(bytes.TrimSpace(typed)) > 0 {
				if len(stack) == 0 {
					return io.ErrUnexpectedEOF
				}
				stack[len(stack)-1].hasText = true
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return io.ErrUnexpectedEOF
			}
			stack = stack[:len(stack)-1]
		}
	}
	if root == nil || len(stack) != 0 {
		return io.ErrUnexpectedEOF
	}
	return walkXMLElement(root, "/"+xmlPathSegment(root.name), add)
}

func walkXMLElement(current *xmlElement, path string, add func(ParameterLocation, string) error) error {
	for _, attribute := range current.attributes {
		if err := add(ParameterXML, path+"/@"+xmlPathSegment(attribute)); err != nil {
			return err
		}
	}
	if len(current.children) == 0 || current.hasText {
		if err := add(ParameterXML, path); err != nil {
			return err
		}
	}
	childCounts := make(map[string]int, len(current.children))
	for _, child := range current.children {
		childCounts[xmlNameKey(child.name)]++
	}
	childIndexes := make(map[string]int, len(childCounts))
	for _, child := range current.children {
		key := xmlNameKey(child.name)
		segment := xmlPathSegment(child.name)
		if childCounts[key] > 1 {
			segment += "/" + strconv.Itoa(childIndexes[key])
		}
		if err := walkXMLElement(child, path+"/"+segment, add); err != nil {
			return err
		}
		childIndexes[key]++
	}
	return nil
}

func xmlNameKey(name xml.Name) string {
	return name.Space + "\x00" + name.Local
}

func xmlPathSegment(name xml.Name) string {
	if name.Space != "" {
		return escapeJSONPointer("{" + name.Space + "}" + name.Local)
	}
	return escapeJSONPointer(name.Local)
}

func addMultipartParameters(body []byte, contentType string, maximumParts int, add func(ParameterLocation, string) error) error {
	_, parameters, err := mime.ParseMediaType(strings.Split(contentType, "\x00")[0])
	if err != nil || parameters["boundary"] == "" {
		return io.ErrUnexpectedEOF
	}
	reader := multipart.NewReader(bytes.NewReader(body), parameters["boundary"])
	for partCount := 0; ; partCount++ {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if partCount >= maximumParts {
			return closeMultipartPart(part, errBodyLimitExceeded)
		}
		name := part.FormName()
		if name == "" {
			return closeMultipartPart(part, io.ErrUnexpectedEOF)
		}
		location := ParameterMultipartField
		if multipartPartHasFilename(part) {
			location = ParameterMultipartFile
		}
		if err := add(location, name); err != nil {
			return closeMultipartPart(part, err)
		}
		if _, err := io.Copy(io.Discard, part); err != nil {
			return closeMultipartPart(part, err)
		}
		if err := part.Close(); err != nil {
			return err
		}
	}
}

func multipartPartHasFilename(part *multipart.Part) bool {
	_, parameters, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		return false
	}
	_, filename := parameters["filename"]
	_, extendedFilename := parameters["filename*"]
	return filename || extendedFilename
}

func closeMultipartPart(part *multipart.Part, cause error) error {
	if err := part.Close(); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func jsonParameterName(path string) string {
	if path == "" {
		return "$"
	}
	return path
}

func escapeJSONPointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}
