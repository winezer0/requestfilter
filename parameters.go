package requestfilter

import (
	"net/url"
	"sort"
	"strings"
)

func extractParameters(request normalizedRequest, options normalizedOptions) ([]Parameter, ParameterStatus, error) {
	parameters := make(map[string]Parameter)
	status := ParameterStatusComplete
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
	parsedURL, err := url.Parse(request.canonicalURL)
	if err != nil {
		return nil, status, ErrInvalidRequest
	}
	if parsedURL.RawQuery != "" {
		query, parseErr := url.ParseQuery(parsedURL.RawQuery)
		if parseErr != nil {
			return sortedParameters(parameters), ParameterStatusPartial, nil
		}
		for name := range query {
			if err := add(ParameterQuery, name); err != nil {
				return sortedParameters(parameters), ParameterStatusPartial, nil
			}
		}
	}
	mediaType := strings.Split(request.mediaType, "\x00")[0]
	bodyMayContainParameters := len(request.request.Body) > 0 || !request.request.BodyCaptured
	bodyTypeSupported := isSupportedBodyMediaType(mediaType)
	if bodyMayContainParameters && !bodyTypeSupported {
		status = ParameterStatusPartial
	}
	if bodyTypeSupported {
		if !request.request.BodyCaptured {
			status = ParameterStatusPartial
		} else {
			bodyParameters, parseErr := parseBodyParameters(request, mediaType, options)
			if parseErr != nil {
				return sortedParameters(parameters), ParameterStatusPartial, nil
			} else {
				for _, parameter := range bodyParameters {
					if err := add(parameter.Location, parameter.Name); err != nil {
						return sortedParameters(parameters), ParameterStatusPartial, nil
					}
				}
			}
		}
	}
	return sortedParameters(parameters), status, nil
}

func sortedParameters(parameters map[string]Parameter) []Parameter {
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
	return result
}
