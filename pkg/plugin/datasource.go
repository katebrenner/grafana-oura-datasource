package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/katebrenner/oura-datasource/pkg/models"
)

// Make sure Datasource implements required interfaces. This is important to do
// since otherwise we will only get a not implemented error response from plugin in
// runtime. In this example datasource instance implements backend.QueryDataHandler,
// backend.CheckHealthHandler interfaces. Plugin should not implement all these
// interfaces - only those which are required for a particular task.
var (
	_ backend.QueryDataHandler       = (*Datasource)(nil)
	_ backend.CheckHealthHandler     = (*Datasource)(nil)
	_ backend.CallResourceHandler    = (*Datasource)(nil)
	_ instancemgmt.InstanceDisposer  = (*Datasource)(nil)
)

// NewDatasource creates a new datasource instance.
func NewDatasource(_ context.Context, _ backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	d := &Datasource{}
	mux := http.NewServeMux()
	// Single handler: Grafana may send path as "oauth/start" or "/oauth/start"; accept both.
	mux.HandleFunc("/", func(rw http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		if path == "" {
			path = "/"
		}
		switch path {
		case "oauth/start", "/oauth/start":
			d.handleOAuthStart(rw, req)
		case "oauth/exchange", "/oauth/exchange":
			d.handleOAuthExchange(rw, req)
		default:
			http.NotFound(rw, req)
		}
	})
	d.resourceHandler = httpadapter.New(mux)
	return d, nil
}

// Datasource is an example datasource which can respond to data queries, reports
// its health and has streaming skills.
type Datasource struct {
	resourceHandler backend.CallResourceHandler
}

// Dispose here tells plugin SDK that plugin wants to clean up resources when a new instance
// created. As soon as datasource settings change detected by SDK old datasource instance will
// be disposed and a new one will be created using NewSampleDatasource factory function.
func (d *Datasource) Dispose() {
	// Clean up datasource instance resources.
}

// CallResource handles resource calls (e.g. OAuth start/callback).
func (d *Datasource) CallResource(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
	return d.resourceHandler.CallResource(ctx, req, sender)
}

// QueryData handles multiple queries and returns multiple responses.
// req contains the queries []DataQuery (where each query contains RefID as a unique identifier).
// The QueryDataResponse contains a map of RefID to the response for each query, and each response
// contains Frames ([]*Frame).
func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	// create response struct
	response := backend.NewQueryDataResponse()

	// loop over queries and execute them individually.
	for _, q := range req.Queries {
		res := d.query(ctx, req.PluginContext, q)

		// save the response in a hashmap
		// based on with RefID as identifier
		response.Responses[q.RefID] = res
	}

	return response, nil
}

type queryModel struct{}

func (d *Datasource) query(_ context.Context, pCtx backend.PluginContext, query backend.DataQuery) backend.DataResponse {
	var response backend.DataResponse

	// Unmarshal the JSON into our queryModel.
	var qm queryModel

	err := json.Unmarshal(query.JSON, &qm)
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("json unmarshal: %v", err.Error()))
	}

	// create data frame response.
	// For an overview on data frames and how grafana handles them:
	// https://grafana.com/developers/plugin-tools/introduction/data-frames
	frame := data.NewFrame("response")

	// add fields.
	frame.Fields = append(frame.Fields,
		data.NewField("time", nil, []time.Time{query.TimeRange.From, query.TimeRange.To}),
		data.NewField("values", nil, []int64{10, 20}),
	)

	// add the frames to the response.
	response.Frames = append(response.Frames, frame)

	return response
}

// CheckHealth handles health checks sent from Grafana to the plugin.
// The main use case for these health checks is the test button on the
// datasource configuration page which allows users to verify that
// a datasource is working as expected.
func (d *Datasource) CheckHealth(_ context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) { // TODO - change health check to check if the user is connected to Oura?
	res := &backend.CheckHealthResult{}
	config, err := models.LoadPluginSettings(*req.PluginContext.DataSourceInstanceSettings)

	if err != nil {
		res.Status = backend.HealthStatusError
		res.Message = "Unable to load settings"
		return res, nil
	}

	hasAuth := config.Secrets != nil && config.Secrets.AccessToken != ""
	if !hasAuth {
		res.Status = backend.HealthStatusError
		res.Message = "Connect with Oura to authorize, then Save again"
		return res, nil
	}

	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "Data source is working",
	}, nil
}

func (d *Datasource) handleOAuthStart(rw http.ResponseWriter, req *http.Request) {
	defer func() {
		if err := recover(); err != nil {
			log.Printf("oura-datasource: handleOAuthStart panic: %v", err)
			http.Error(rw, fmt.Sprintf("plugin error: %v", err), http.StatusInternalServerError)
		}
	}()
	pCtx := backend.PluginConfigFromContext(req.Context())
	if pCtx.DataSourceInstanceSettings == nil {
		http.Error(rw, "missing plugin context", http.StatusBadRequest)
		return
	}
	config, err := models.LoadPluginSettings(*pCtx.DataSourceInstanceSettings)
	if err != nil {
		log.Printf("oura-datasource: LoadPluginSettings: %v", err)
		http.Error(rw, "failed to load settings", http.StatusInternalServerError)
		return
	}
	redirectURI := req.URL.Query().Get("redirect_uri")
	if redirectURI == "" {
		http.Error(rw, "redirect_uri required", http.StatusBadRequest)
		return
	}
	if config.ClientId == "" || config.Secrets == nil || config.Secrets.ClientSecret == "" {
		http.Error(rw, "client ID and client secret must be configured", http.StatusBadRequest)
		return
	}
	// Use state to pass redirect_uri back to callback (needed for token exchange).
	authURL := buildAuthorizeURL(config.ClientId, redirectURI, redirectURI)
	http.Redirect(rw, req, authURL, http.StatusFound)
}

// handleOAuthExchange exchanges an authorization code for tokens (Strava-style flow).
// Called by the frontend when the config page loads with ?code=... after Oura redirect.
// POST body or GET query: code, redirect_uri (must match the redirect_uri used in the authorize request).
func (d *Datasource) handleOAuthExchange(rw http.ResponseWriter, req *http.Request) {
	defer func() {
		if err := recover(); err != nil {
			log.Printf("oura-datasource: handleOAuthExchange panic: %v", err)
			http.Error(rw, fmt.Sprintf("plugin error: %v", err), http.StatusInternalServerError)
		}
	}()
	pCtx := backend.PluginConfigFromContext(req.Context())
	if pCtx.DataSourceInstanceSettings == nil {
		http.Error(rw, "missing plugin context", http.StatusBadRequest)
		return
	}
	config, err := models.LoadPluginSettings(*pCtx.DataSourceInstanceSettings)
	if err != nil {
		log.Printf("oura-datasource: LoadPluginSettings: %v", err)
		http.Error(rw, "failed to load settings", http.StatusInternalServerError)
		return
	}
	var code, redirectURI string
	if req.Method == http.MethodPost && req.Header.Get("Content-Type") == "application/json" {
		var body struct {
			Code        string `json:"code"`
			RedirectURI string `json:"redirect_uri"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(rw, "invalid JSON", http.StatusBadRequest)
			return
		}
		code, redirectURI = body.Code, body.RedirectURI
	} else if req.Method == http.MethodPost {
		if err := req.ParseForm(); err != nil {
			http.Error(rw, "invalid form", http.StatusBadRequest)
			return
		}
		code = req.PostForm.Get("code")
		redirectURI = req.PostForm.Get("redirect_uri")
	} else {
		code = req.URL.Query().Get("code")
		redirectURI = req.URL.Query().Get("redirect_uri")
	}
	if code == "" || redirectURI == "" {
		http.Error(rw, "code and redirect_uri required", http.StatusBadRequest)
		return
	}
	if config.ClientId == "" || config.Secrets == nil || config.Secrets.ClientSecret == "" {
		http.Error(rw, "client ID and client secret not configured", http.StatusBadRequest)
		return
	}
	accessToken, refreshToken, err := exchangeCodeForToken(req.Context(), config.ClientId, config.Secrets.ClientSecret, redirectURI, code)
	if err != nil {
		msg := "token exchange failed: " + err.Error()
		if strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "Invalid") {
			msg += ". Ensure the redirect URI in Oura's app settings matches exactly: " + redirectURI
		}
		http.Error(rw, msg, http.StatusBadRequest)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(map[string]string{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
	})
}
