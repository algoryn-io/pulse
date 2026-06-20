#ifndef PULSE_INTERNAL_STATS_ENGINE_HPP
#define PULSE_INTERNAL_STATS_ENGINE_HPP

#include <array>
#include <cstdint>

namespace pulse {
namespace stats {

// Logarithmic histogram (log10-spaced bin edges) from 1 microsecond to 60 seconds.
// Bins are uniform in log10(latencyns) so each bucket is roughly 1/3 of a decade wide,
// i.e. about three significant decimal digits of relative resolution in log space
// (800 buckets over ~7.78 decades: step ~0.01 in log10).
class StatsEngine {
 public:
  static constexpr int kNumBuckets = 800;
  static constexpr int64_t kMinLatencyNs = 1'000;                  // 1 microsecond
  static constexpr int64_t kMaxLatencyNs = 60'000'000'000LL;     // 60 seconds

  StatsEngine();

  void RecordLatency(int64_t nanos);
  // Returns the estimated latency in nanoseconds for 0-100. Returns 0 if empty.
  double GetPercentile(double p) const;
  void Reset();

  uint64_t Total() const { return total_; }

  // Copies all kNumBuckets bucket counts into out[0..n-1]. n must equal kNumBuckets.
  // Used by distributed workers to ship histogram state to the coordinator.
  void ExportBuckets(uint64_t* out, int n) const;

  // Adds counts from in[0..n-1] into this engine's buckets and updates total_.
  // n must equal kNumBuckets. Used by the coordinator to merge worker histograms.
  void ImportBuckets(const uint64_t* in, int n);

 private:
  int BucketIndex(int64_t nanos) const;
  // Lower bound of bucket i, i in [0, kNumBuckets); upper bound of last bucket is kMaxLatencyNs.
  double LowerEdgeNs(int i) const;

  std::array<uint64_t, kNumBuckets> counts_{};
  uint64_t total_{0};

  // Precomputed: log10(kMinLatencyNs) and 1/((log10(kMax)-log10(kMin))/kNumBuckets) for O(1) bucket.
  double log_min_{0.0};
  double log_max_{0.0};
  double inv_log_step_{0.0};  // 1.0 / per-bin log10 width
};

}  // namespace stats
}  // namespace pulse

#endif  // PULSE_INTERNAL_STATS_ENGINE_HPP
