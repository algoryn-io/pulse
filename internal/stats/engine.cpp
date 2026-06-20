#include "engine.hpp"

#include <algorithm>
#include <cmath>

namespace pulse {
namespace stats {

namespace {

double Log10I64Clamped(int64_t v) {
  if (v < 1) {
    return std::log10(1.0);
  }
  return std::log10(static_cast<double>(v));
}

}  // namespace

StatsEngine::StatsEngine() {
  log_min_ = std::log10(static_cast<double>(kMinLatencyNs));
  log_max_ = std::log10(static_cast<double>(kMaxLatencyNs));
  const double log_span = log_max_ - log_min_;
  if (log_span > 0.0) {
    inv_log_step_ = static_cast<double>(kNumBuckets) / log_span;
  } else {
    inv_log_step_ = 0.0;
  }
}

int StatsEngine::BucketIndex(int64_t nanos) const {
  if (nanos >= kMaxLatencyNs) {
    return kNumBuckets - 1;
  }
  if (nanos <= 0) {
    return 0;
  }
  if (nanos < kMinLatencyNs) {
    return 0;
  }
  const double lg = Log10I64Clamped(nanos);
  if (lg <= log_min_) {
    return 0;
  }
  if (lg >= log_max_) {
    return kNumBuckets - 1;
  }
  int idx = static_cast<int>(std::floor((lg - log_min_) * inv_log_step_));
  if (idx < 0) {
    return 0;
  }
  if (idx >= kNumBuckets) {
    return kNumBuckets - 1;
  }
  return idx;
}

double StatsEngine::LowerEdgeNs(int i) const {
  if (i < 0) {
    return static_cast<double>(kMinLatencyNs);
  }
  if (i >= kNumBuckets) {
    return static_cast<double>(kMaxLatencyNs);
  }
  const double w = (log_max_ - log_min_) / static_cast<double>(kNumBuckets);
  return std::pow(10.0, log_min_ + w * static_cast<double>(i));
}

void StatsEngine::RecordLatency(int64_t nanos) {
  int idx = BucketIndex(nanos);
  counts_[static_cast<std::size_t>(idx)]++;
  total_++;
}

void StatsEngine::Reset() {
  counts_.fill(0U);
  total_ = 0;
}

void StatsEngine::ExportBuckets(uint64_t* out, int n) const {
  if (out == nullptr || n != kNumBuckets) {
    return;
  }
  for (int i = 0; i < kNumBuckets; i++) {
    out[static_cast<std::size_t>(i)] = counts_[static_cast<std::size_t>(i)];
  }
}

void StatsEngine::ImportBuckets(const uint64_t* in, int n) {
  if (in == nullptr || n != kNumBuckets) {
    return;
  }
  for (int i = 0; i < kNumBuckets; i++) {
    const uint64_t v = in[static_cast<std::size_t>(i)];
    counts_[static_cast<std::size_t>(i)] += v;
    total_ += v;
  }
}

double StatsEngine::GetPercentile(double p) const {
  if (total_ == 0) {
    return 0.0;
  }
  p = std::max(0.0, std::min(100.0, p));
  // Nearest-rank: smallest value v such that at least ceil(p/100*total) samples are <= v
  // (we approximate the continuous rank inside the final bucket with linear CDF in log space).
  const double rank = std::max(1.0, std::ceil(p * static_cast<double>(total_) / 100.0));
  const double t = rank;  // 1-based rank in [1, total_]

  uint64_t acc = 0;
  for (int i = 0; i < kNumBuckets; i++) {
    const uint64_t c = counts_[static_cast<std::size_t>(i)];
    if (c == 0) {
      continue;
    }
    if (acc + c >= static_cast<uint64_t>(t)) {
      // Interpolate in log space between bucket edges: uniform within bucket.
      const double l0 = std::log(LowerEdgeNs(i));
      const double l1 = (i + 1 < kNumBuckets) ? std::log(LowerEdgeNs(i + 1)) : std::log(static_cast<double>(kMaxLatencyNs));
      const uint64_t before = acc;
      const uint64_t need = static_cast<uint64_t>(t) - before;  // 1..c
      const double f = (static_cast<double>(need) - 0.5) / static_cast<double>(c);
      const double w = std::min(1.0, std::max(0.0, f));
      return std::exp(l0 + w * (l1 - l0));
    }
    acc += c;
  }
  // Fallback: all mass at last bucket
  return static_cast<double>(kMaxLatencyNs);
}

}  // namespace stats
}  // namespace pulse

extern "C" {

// C ABI for cgo. Do not use from C++ except via Go bindings.

struct PulseStatsHandle {
  pulse::stats::StatsEngine* engine;
};

void* pulse_stats_engine_create(void) {
  auto* h = new PulseStatsHandle;
  h->engine = new pulse::stats::StatsEngine();
  return h;
}

void pulse_stats_engine_destroy(void* p) {
  if (p == nullptr) {
    return;
  }
  auto* h = static_cast<PulseStatsHandle*>(p);
  delete h->engine;
  delete h;
}

void pulse_stats_engine_record(void* p, long long nanos) {
  if (p == nullptr) {
    return;
  }
  static_cast<PulseStatsHandle*>(p)->engine->RecordLatency(nanos);
}

double pulse_stats_engine_get_percentile(void* p, double percent) {
  if (p == nullptr) {
    return 0.0;
  }
  return static_cast<PulseStatsHandle*>(p)->engine->GetPercentile(percent);
}

void pulse_stats_engine_reset(void* p) {
  if (p == nullptr) {
    return;
  }
  static_cast<PulseStatsHandle*>(p)->engine->Reset();
}

unsigned long long pulse_stats_engine_total(void* p) {
  if (p == nullptr) {
    return 0U;
  }
  return static_cast<PulseStatsHandle*>(p)->engine->Total();
}

int pulse_stats_engine_num_buckets(void) {
  return pulse::stats::StatsEngine::kNumBuckets;
}

// Copies the kNumBuckets bucket counts into out[0..kNumBuckets-1].
// out must point to a buffer of at least kNumBuckets unsigned long long values.
void pulse_stats_engine_export_buckets(void* p, unsigned long long* out, int n) {
  if (p == nullptr) {
    return;
  }
  static_cast<PulseStatsHandle*>(p)->engine->ExportBuckets(
      reinterpret_cast<uint64_t*>(out), n);
}

// Adds counts from in[0..n-1] into the engine's buckets and updates total_.
// n must equal kNumBuckets; mismatched n is silently ignored.
void pulse_stats_engine_import_buckets(void* p, const unsigned long long* in, int n) {
  if (p == nullptr) {
    return;
  }
  static_cast<PulseStatsHandle*>(p)->engine->ImportBuckets(
      reinterpret_cast<const uint64_t*>(in), n);
}

}  // extern "C"
