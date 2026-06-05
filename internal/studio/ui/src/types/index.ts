export interface Metric {
  name: string;
  description: string;
  unit: string;
  type: string;
}

export interface MetricPoint {
  metric_name: string;
  timestamp_nano: number;
  value: number;
  attributes: Record<string, unknown>;
}

export interface Thread {
  thread_id: string;
  graph_name: string;
  start_time: number;
  trace_count: number;
  total_tokens: number;
  has_error: boolean;
  invocation_count: number;
}

export interface Span {
  trace_id: string;
  span_id: string;
  parent_span_id?: string;
  name: string;
  kind: string;
  start_time_nano: number;
  end_time_nano: number;
  attributes: Record<string, unknown>;
  status_code: string;
  status_message?: string;
}

export interface DashboardStats {
  total_threads: number;
  total_spans: number;
  total_tokens: number;
  error_count: number;
  llm_call_count: number;
  p50_latency: number;
}
