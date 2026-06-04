package studio

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"

	metricv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	mv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
)

type OTLPReceiver struct {
	db *DB
}

func NewOTLPReceiver(db *DB) *OTLPReceiver {
	return &OTLPReceiver{db: db}
}

func (r *OTLPReceiver) StartGRPC(port int) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}

	s := grpc.NewServer()
	tracev1.RegisterTraceServiceServer(s, &traceServer{db: r.db})
	metricv1.RegisterMetricsServiceServer(s, &metricServer{db: r.db})

	fmt.Printf("OTLP gRPC receiver listening on :%d\n", port)
	return s.Serve(lis)
}

func (r *OTLPReceiver) StartHTTP(port int) error {
	mux := http.NewServeMux()
	ts := &traceServer{db: r.db}
	ms := &metricServer{db: r.db}

	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var exportReq tracev1.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &exportReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ts.Export(req.Context(), &exportReq)
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/v1/metrics", func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var exportReq metricv1.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &exportReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ms.Export(req.Context(), &exportReq)
		w.WriteHeader(http.StatusOK)
	})

	fmt.Printf("OTLP HTTP receiver listening on :%d\n", port)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), mux)
}

type traceServer struct {
	tracev1.UnimplementedTraceServiceServer
	db *DB
}

func (s *traceServer) Export(ctx context.Context, req *tracev1.ExportTraceServiceRequest) (*tracev1.ExportTraceServiceResponse, error) {
	for _, resSpans := range req.ResourceSpans {
		resAttrs := mapAttributes(resSpans.Resource.Attributes)
		for _, scopeSpans := range resSpans.ScopeSpans {
			for _, span := range scopeSpans.Spans {
				record := SpanRecord{
					TraceID:       hex.EncodeToString(span.TraceId),
					SpanID:        hex.EncodeToString(span.SpanId),
					ParentSpanID:  hex.EncodeToString(span.ParentSpanId),
					Name:          span.Name,
					Kind:          span.Kind.String(),
					StartTimeNano: int64(span.StartTimeUnixNano),
					EndTimeNano:   int64(span.EndTimeUnixNano),
					Attributes:    mapAttributes(span.Attributes),
					StatusCode:    span.Status.Code.String(),
					StatusMessage: span.Status.Message,
				}
				// Merge resource attributes
				for k, v := range resAttrs {
					if _, ok := record.Attributes[k]; !ok {
						record.Attributes[k] = v
					}
				}

				if err := s.db.InsertSpan(ctx, record); err != nil {
					fmt.Printf("Failed to insert span: %v\n", err)
				}
			}
		}
	}
	return &tracev1.ExportTraceServiceResponse{}, nil
}

type metricServer struct {
	metricv1.UnimplementedMetricsServiceServer
	db *DB
}

func (s *metricServer) Export(ctx context.Context, req *metricv1.ExportMetricsServiceRequest) (*metricv1.ExportMetricsServiceResponse, error) {
	for _, resMetrics := range req.ResourceMetrics {
		for _, scopeMetrics := range resMetrics.ScopeMetrics {
			for _, metric := range scopeMetrics.Metrics {
				s.db.InsertMetric(ctx, MetricRecord{
					Name:        metric.Name,
					Description: metric.Description,
					Unit:        metric.Unit,
					Type:        fmt.Sprintf("%T", metric.Data),
				})

				switch data := metric.Data.(type) {
				case *mv1.Metric_Sum:
					for _, dp := range data.Sum.DataPoints {
						s.db.InsertMetricPoint(ctx, MetricPoint{
							MetricName:    metric.Name,
							TimestampNano: int64(dp.TimeUnixNano),
							Value:         getDPValue(dp),
							Attributes:    mapAttributes(dp.Attributes),
						})
					}
				case *mv1.Metric_Histogram:
					for _, dp := range data.Histogram.DataPoints {
						s.db.InsertMetricPoint(ctx, MetricPoint{
							MetricName:    metric.Name,
							TimestampNano: int64(dp.TimeUnixNano),
							Value:         dp.GetSum(),
							Attributes:    mapAttributes(dp.Attributes),
						})
					}
				}
			}
		}
	}
	return &metricv1.ExportMetricsServiceResponse{}, nil
}

func mapAttributes(attrs []*commonv1.KeyValue) map[string]any {
	res := make(map[string]any)
	for _, attr := range attrs {
		res[attr.Key] = getAnyValue(attr.Value)
	}
	return res
}

func getAnyValue(v *commonv1.AnyValue) any {
	if v == nil {
		return nil
	}
	switch val := v.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return val.StringValue
	case *commonv1.AnyValue_IntValue:
		return val.IntValue
	case *commonv1.AnyValue_DoubleValue:
		return val.DoubleValue
	case *commonv1.AnyValue_BoolValue:
		return val.BoolValue
	case *commonv1.AnyValue_ArrayValue:
		var arr []any
		for _, item := range val.ArrayValue.Values {
			arr = append(arr, getAnyValue(item))
		}
		return arr
	case *commonv1.AnyValue_KvlistValue:
		return mapAttributes(val.KvlistValue.Values)
	default:
		return nil
	}
}

func getDPValue(dp *mv1.NumberDataPoint) float64 {
	switch val := dp.Value.(type) {
	case *mv1.NumberDataPoint_AsDouble:
		return val.AsDouble
	case *mv1.NumberDataPoint_AsInt:
		return float64(val.AsInt)
	default:
		return 0
	}
}
