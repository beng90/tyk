package oas

import (
	"encoding/json"
	logger "github.com/TykTechnologies/tyk/log"
	"github.com/xeipuuv/gojsonschema"
	"io/ioutil"
	"os"
	"strings"

	"github.com/TykTechnologies/tyk/apidef"
	"github.com/getkin/kin-openapi/openapi3"
)

const ExtensionTykAPIGateway = "x-tyk-api-gateway"
const Main = ""

var log = logger.Get()
var oasJSONSchemas map[string][]byte

func init() {
	oasJSONSchemas = make(map[string][]byte)
	baseDir := "./schema/"
	files, err := os.ReadDir(baseDir)

	if err != nil {
		log.WithError(err).Error("error while listing schema files")
		return
	}

	for _, fileInfo := range files {
		if fileInfo.IsDir() {
			continue
		}

		oasVersion := strings.TrimSuffix(fileInfo.Name(), ".json")
		file, err := os.Open(baseDir + fileInfo.Name())
		if err != nil {
			log.WithError(err).Error("error while loading oas json schema")
			continue
		}

		oasJSONSchema, err := ioutil.ReadAll(file)
		if err != nil {
			log.WithError(err).Error("error while reading schema file")
			continue
		}

		oasJSONSchemas[oasVersion] = oasJSONSchema
	}

}

func ValidateOASObject(documentBody []byte, oasVersion string) (bool, []string) {
	schemaLoader := gojsonschema.NewBytesLoader(oasJSONSchemas[oasVersion])
	documentLoader := gojsonschema.NewBytesLoader(documentBody)
	result, err := gojsonschema.Validate(schemaLoader, documentLoader)

	if err != nil {
		log.WithError(err).Errorln("error while validating document")
		return false, nil
	}

	if !result.Valid() {
		log.Error("OAS object validation failed, most likely malformed input")
		validationErrs := result.Errors()
		var errs = make([]string, len(validationErrs))
		for i, validationErr := range validationErrs {
			errStr := validationErr.String()
			errs[i] = errStr
		}
		return false, errs
	}

	return true, nil
}

type OAS struct {
	openapi3.T
}

func (s *OAS) Fill(api apidef.APIDefinition) {
	xTykAPIGateway := s.GetTykExtension()
	if xTykAPIGateway == nil {
		xTykAPIGateway = &XTykAPIGateway{}
		s.SetTykExtension(xTykAPIGateway)
	}

	xTykAPIGateway.Fill(api)
	s.fillPathsAndOperations(api.VersionData.Versions[Main].ExtendedPaths)
	s.fillSecurity(api)

	if ShouldOmit(xTykAPIGateway) {
		delete(s.Extensions, ExtensionTykAPIGateway)
	}

	if ShouldOmit(s.Extensions) {
		s.Extensions = nil
	}
}

func (s *OAS) ExtractTo(api *apidef.APIDefinition) {
	if s.Security != nil {
		s.extractSecurityTo(api)
	} else {
		api.UseKeylessAccess = true
	}

	if s.GetTykExtension() != nil {
		s.GetTykExtension().ExtractTo(api)
	}

	var ep apidef.ExtendedPathsSet
	s.extractPathsAndOperations(&ep)

	api.VersionData.Versions = map[string]apidef.VersionInfo{
		Main: {
			UseExtendedPaths: true,
			ExtendedPaths:    ep,
		},
	}
}

func (s *OAS) SetTykExtension(xTykAPIGateway *XTykAPIGateway) {
	if s.Extensions == nil {
		s.Extensions = make(map[string]interface{})
	}

	s.Extensions[ExtensionTykAPIGateway] = xTykAPIGateway
}

func (s *OAS) GetTykExtension() *XTykAPIGateway {
	if s.Extensions == nil {
		return nil
	}

	if ext := s.Extensions[ExtensionTykAPIGateway]; ext != nil {
		rawTykAPIGateway, ok := ext.(json.RawMessage)
		if ok {
			var xTykAPIGateway XTykAPIGateway
			_ = json.Unmarshal(rawTykAPIGateway, &xTykAPIGateway)
			s.Extensions[ExtensionTykAPIGateway] = &xTykAPIGateway
			return &xTykAPIGateway
		}

		mapTykAPIGateway, ok := ext.(map[string]interface{})
		if ok {
			var xTykAPIGateway XTykAPIGateway
			dbByte, _ := json.Marshal(mapTykAPIGateway)
			_ = json.Unmarshal(dbByte, &xTykAPIGateway)
			s.Extensions[ExtensionTykAPIGateway] = &xTykAPIGateway
			return &xTykAPIGateway
		}

		return ext.(*XTykAPIGateway)
	}

	return nil
}

func (s *OAS) getTykAuthentication() (authentication *Authentication) {
	if s.GetTykExtension() != nil {
		authentication = s.GetTykExtension().Server.Authentication
	}

	return
}

func (s *OAS) getTykTokenAuth(name string) (token *Token) {
	if securitySchemes := s.getTykSecuritySchemes(); securitySchemes != nil {
		securityScheme := securitySchemes[name]
		if securityScheme == nil {
			return
		}

		mapSecurityScheme, ok := securityScheme.(map[string]interface{})
		if ok {
			token = &Token{}
			inBytes, _ := json.Marshal(mapSecurityScheme)
			_ = json.Unmarshal(inBytes, token)
			s.getTykSecuritySchemes()[name] = token
			return
		}

		token = s.getTykSecuritySchemes()[name].(*Token)
	}

	return
}

func (s *OAS) getTykJWTAuth(name string) (jwt *JWT) {
	securityScheme := s.getTykSecurityScheme(name)
	if securityScheme == nil {
		return
	}

	mapSecurityScheme, ok := securityScheme.(map[string]interface{})
	if ok {
		jwt = &JWT{}
		inBytes, _ := json.Marshal(mapSecurityScheme)
		_ = json.Unmarshal(inBytes, jwt)
		s.getTykSecuritySchemes()[name] = jwt
		return
	}

	jwt = securityScheme.(*JWT)

	return
}

func (s *OAS) getTykBasicAuth(name string) (basic *Basic) {
	securityScheme := s.getTykSecurityScheme(name)
	if securityScheme == nil {
		return
	}

	mapSecurityScheme, ok := securityScheme.(map[string]interface{})
	if ok {
		basic = &Basic{}
		inBytes, _ := json.Marshal(mapSecurityScheme)
		_ = json.Unmarshal(inBytes, basic)
		s.getTykSecuritySchemes()[name] = basic
		return
	}

	basic = securityScheme.(*Basic)

	return
}

func (s *OAS) getTykOAuthAuth(name string) (oAuth *OAuth) {
	if securitySchemes := s.getTykSecuritySchemes(); securitySchemes != nil {
		securityScheme := securitySchemes[name]
		if securityScheme == nil {
			return
		}

		mapSecurityScheme, ok := securityScheme.(map[string]interface{})
		if ok {
			oAuth = &OAuth{}
			inBytes, _ := json.Marshal(mapSecurityScheme)
			_ = json.Unmarshal(inBytes, oAuth)
			s.getTykSecuritySchemes()[name] = oAuth
			return
		}

		oAuth = s.getTykSecuritySchemes()[name].(*OAuth)
	}

	return
}

func (s *OAS) getTykSecuritySchemes() (securitySchemes map[string]interface{}) {
	if s.getTykAuthentication() != nil {
		securitySchemes = s.getTykAuthentication().SecuritySchemes
	}

	return
}

func (s *OAS) getTykSecurityScheme(name string) interface{} {
	securitySchemes := s.getTykSecuritySchemes()
	if securitySchemes == nil {
		return nil
	}

	return securitySchemes[name]
}

func (s *OAS) getTykMiddleware() (middleware *Middleware) {
	if s.GetTykExtension() != nil {
		middleware = s.GetTykExtension().Middleware
	}

	return
}

func (s *OAS) getTykOperations() (operations Operations) {
	if s.getTykMiddleware() != nil {
		operations = s.getTykMiddleware().Operations
	}

	return
}
