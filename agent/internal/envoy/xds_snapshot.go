package envoy

import (
	"fmt"
	"time"

	accesslogv3 "github.com/envoyproxy/go-control-plane/envoy/config/accesslog/v3"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	fileaccesslogv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/access_loggers/file/v3"
	routerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	cachetypes "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// snapshotParams holds the full desired configuration for an xDS snapshot.
type snapshotParams struct {
	port        uint16
	clusterName string
	directDNS   *directDNSListenerConfig // nil when not in direct_dns mode
	endpoint    *endpointState           // nil when no upstream is assigned yet
}

type endpointState struct {
	address string
	port    uint16
}

func buildSnapshot(version string, p snapshotParams) (*cachev3.Snapshot, error) {
	listeners, err := buildListeners(p)
	if err != nil {
		return nil, fmt.Errorf("build listeners: %w", err)
	}
	cluster, err := buildCluster(p.clusterName)
	if err != nil {
		return nil, fmt.Errorf("build cluster: %w", err)
	}
	endpoint := buildEndpoint(p.clusterName, p.endpoint)

	resources := map[resourcev3.Type][]cachetypes.Resource{
		resourcev3.ListenerType: listeners,
		resourcev3.ClusterType:  {cluster},
		resourcev3.EndpointType: {endpoint},
	}
	snap, err := cachev3.NewSnapshot(version, resources)
	if err != nil {
		return nil, fmt.Errorf("new snapshot: %w", err)
	}
	return snap, nil
}

func buildListeners(p snapshotParams) ([]cachetypes.Resource, error) {
	httpListener, err := buildHTTPListener(p.port, p.clusterName)
	if err != nil {
		return nil, err
	}
	listeners := []cachetypes.Resource{httpListener}
	if p.directDNS != nil {
		redirectListener, err := buildHTTPRedirectListener(p.directDNS.HTTPPort)
		if err != nil {
			return nil, err
		}
		httpsListener, err := buildHTTPSListener(p.directDNS, p.clusterName)
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, redirectListener, httpsListener)
	}
	return listeners, nil
}

func buildHTTPListener(port uint16, clusterName string) (*listenerv3.Listener, error) {
	hcmAny, err := buildHCMAny("ingress_http", "local_route", clusterName, accessLogFormat, nil, "")
	if err != nil {
		return nil, err
	}
	return &listenerv3.Listener{
		Name:    "listener_http",
		Address: socketAddress("0.0.0.0", port),
		FilterChains: []*listenerv3.FilterChain{{
			Filters: []*listenerv3.Filter{hcmFilter(hcmAny)},
		}},
	}, nil
}

func buildHTTPRedirectListener(port uint16) (*listenerv3.Listener, error) {
	hcmAny, err := buildRedirectHCMAny("ingress_public_http", accessLogFormat)
	if err != nil {
		return nil, err
	}
	return &listenerv3.Listener{
		Name:    "listener_public_http",
		Address: socketAddress("0.0.0.0", port),
		FilterChains: []*listenerv3.FilterChain{{
			Filters: []*listenerv3.Filter{hcmFilter(hcmAny)},
		}},
	}, nil
}

func buildHTTPSListener(listener *directDNSListenerConfig, clusterName string) (*listenerv3.Listener, error) {
	hcmAny, err := buildHCMAny(
		"ingress_public_https",
		"public_route_https",
		clusterName,
		accessLogFormat,
		[]*corev3.HeaderValueOption{overwriteRequestHeader("x-forwarded-proto", "https")},
		"https",
	)
	if err != nil {
		return nil, err
	}
	downstreamTLS := &tlsv3.DownstreamTlsContext{
		CommonTlsContext: &tlsv3.CommonTlsContext{
			TlsCertificates: []*tlsv3.TlsCertificate{{
				CertificateChain: &corev3.DataSource{
					Specifier: &corev3.DataSource_InlineBytes{InlineBytes: listener.CertificatePEM},
				},
				PrivateKey: &corev3.DataSource{
					Specifier: &corev3.DataSource_InlineBytes{InlineBytes: listener.PrivateKeyPEM},
				},
			}},
		},
	}
	tlsAny, err := anypb.New(downstreamTLS)
	if err != nil {
		return nil, fmt.Errorf("marshal tls context: %w", err)
	}
	return &listenerv3.Listener{
		Name:    "listener_public_https",
		Address: socketAddress("0.0.0.0", listener.HTTPSPort),
		FilterChains: []*listenerv3.FilterChain{{
			TransportSocket: &corev3.TransportSocket{
				Name: "envoy.transport_sockets.tls",
				ConfigType: &corev3.TransportSocket_TypedConfig{
					TypedConfig: tlsAny,
				},
			},
			Filters: []*listenerv3.Filter{hcmFilter(hcmAny)},
		}},
	}, nil
}

func buildCluster(clusterName string) (*clusterv3.Cluster, error) {
	return &clusterv3.Cluster{
		Name:                 clusterName,
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_EDS},
		EdsClusterConfig: &clusterv3.Cluster_EdsClusterConfig{
			EdsConfig: &corev3.ConfigSource{
				ConfigSourceSpecifier: &corev3.ConfigSource_Ads{
					Ads: &corev3.AggregatedConfigSource{},
				},
				ResourceApiVersion: corev3.ApiVersion_V3,
			},
		},
		ConnectTimeout: durationpb.New(time.Second),
		LbPolicy:       clusterv3.Cluster_ROUND_ROBIN,
	}, nil
}

func buildEndpoint(clusterName string, ep *endpointState) *endpointv3.ClusterLoadAssignment {
	cla := &endpointv3.ClusterLoadAssignment{
		ClusterName: clusterName,
		Endpoints:   []*endpointv3.LocalityLbEndpoints{},
	}
	if ep != nil {
		cla.Endpoints = []*endpointv3.LocalityLbEndpoints{{
			LbEndpoints: []*endpointv3.LbEndpoint{{
				HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
					Endpoint: &endpointv3.Endpoint{
						Address: socketAddress(ep.address, ep.port),
					},
				},
			}},
		}}
	}
	return cla
}

// buildHCMAny builds a marshaled HttpConnectionManager for use as a filter typed_config.
func buildHCMAny(statPrefix, routeName, clusterName, logFormat string, requestHeaders []*corev3.HeaderValueOption, schemeToOverwrite string) (*anypb.Any, error) {
	alAny, err := buildAccessLogAny(logFormat)
	if err != nil {
		return nil, err
	}
	routerAny, err := anypb.New(&routerv3.Router{})
	if err != nil {
		return nil, fmt.Errorf("marshal router: %w", err)
	}
	virtualHost := &routev3.VirtualHost{
		Name:    "web",
		Domains: []string{"*"},
		Routes: []*routev3.Route{{
			Match: &routev3.RouteMatch{
				PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"},
			},
			Action: &routev3.Route_Route{
				Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: clusterName},
				},
			},
		}},
	}
	if len(requestHeaders) > 0 {
		virtualHost.RequestHeadersToAdd = requestHeaders
	}
	hcm := &hcmv3.HttpConnectionManager{
		StatPrefix: statPrefix,
		RouteSpecifier: &hcmv3.HttpConnectionManager_RouteConfig{
			RouteConfig: &routev3.RouteConfiguration{
				Name: routeName,
				VirtualHosts: []*routev3.VirtualHost{virtualHost},
			},
		},
		HttpFilters: []*hcmv3.HttpFilter{{
			Name:       "envoy.filters.http.router",
			ConfigType: &hcmv3.HttpFilter_TypedConfig{TypedConfig: routerAny},
		}},
		AccessLog: []*accesslogv3.AccessLog{{
			Name:       "envoy.access_loggers.file",
			ConfigType: &accesslogv3.AccessLog_TypedConfig{TypedConfig: alAny},
		}},
	}
	if schemeToOverwrite != "" {
		hcm.SchemeHeaderTransformation = &corev3.SchemeHeaderTransformation{
			Transformation: &corev3.SchemeHeaderTransformation_SchemeToOverwrite{
				SchemeToOverwrite: schemeToOverwrite,
			},
		}
	}
	return anypb.New(hcm)
}

func buildRedirectHCMAny(statPrefix, logFormat string) (*anypb.Any, error) {
	alAny, err := buildAccessLogAny(logFormat)
	if err != nil {
		return nil, err
	}
	routerAny, err := anypb.New(&routerv3.Router{})
	if err != nil {
		return nil, fmt.Errorf("marshal router: %w", err)
	}
	hcm := &hcmv3.HttpConnectionManager{
		StatPrefix: statPrefix,
		RouteSpecifier: &hcmv3.HttpConnectionManager_RouteConfig{
			RouteConfig: &routev3.RouteConfiguration{
				Name: "redirect_route",
				VirtualHosts: []*routev3.VirtualHost{{
					Name:    "redirect",
					Domains: []string{"*"},
					Routes: []*routev3.Route{{
						Match: &routev3.RouteMatch{
							PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"},
						},
						Action: &routev3.Route_Redirect{
							Redirect: &routev3.RedirectAction{
								SchemeRewriteSpecifier: &routev3.RedirectAction_HttpsRedirect{
									HttpsRedirect: true,
								},
							},
						},
					}},
				}},
			},
		},
		HttpFilters: []*hcmv3.HttpFilter{{
			Name:       "envoy.filters.http.router",
			ConfigType: &hcmv3.HttpFilter_TypedConfig{TypedConfig: routerAny},
		}},
		AccessLog: []*accesslogv3.AccessLog{{
			Name:       "envoy.access_loggers.file",
			ConfigType: &accesslogv3.AccessLog_TypedConfig{TypedConfig: alAny},
		}},
	}
	return anypb.New(hcm)
}

func buildAccessLogAny(logFormat string) (*anypb.Any, error) {
	al := &fileaccesslogv3.FileAccessLog{
		Path: "/dev/stdout",
		AccessLogFormat: &fileaccesslogv3.FileAccessLog_LogFormat{
			LogFormat: &corev3.SubstitutionFormatString{
				Format: &corev3.SubstitutionFormatString_TextFormatSource{
					TextFormatSource: &corev3.DataSource{
						Specifier: &corev3.DataSource_InlineString{
							InlineString: logFormat,
						},
					},
				},
			},
		},
	}
	return anypb.New(al)
}

func overwriteRequestHeader(key, value string) *corev3.HeaderValueOption {
	return &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:   key,
			Value: value,
		},
		AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
	}
}

func socketAddress(host string, port uint16) *corev3.Address {
	return &corev3.Address{
		Address: &corev3.Address_SocketAddress{
			SocketAddress: &corev3.SocketAddress{
				Address: host,
				PortSpecifier: &corev3.SocketAddress_PortValue{
					PortValue: uint32(port),
				},
			},
		},
	}
}

func hcmFilter(hcmAny *anypb.Any) *listenerv3.Filter {
	return &listenerv3.Filter{
		Name:       "envoy.filters.network.http_connection_manager",
		ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
	}
}

const accessLogFormat = "[%START_TIME%] %REQ(:METHOD)% %REQ(X-ENVOY-ORIGINAL-PATH?:PATH)% %PROTOCOL% %RESPONSE_CODE% %RESPONSE_FLAGS% %BYTES_RECEIVED% %BYTES_SENT% %DURATION% %UPSTREAM_HOST%\n"
