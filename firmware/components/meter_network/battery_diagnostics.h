#pragma once
#include <algorithm>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <cstdio>

// Observe BAT_ADC without inferring battery presence or charger state.
namespace meter::battery {
struct Window {
  uint64_t start_ms = 0, end_ms = 0;
  uint32_t valid = 0, invalid = 0;
  float minimum = NAN, maximum = NAN, first = NAN, last = NAN;
  double sum = 0;
  float mean() const { return valid ? static_cast<float>(sum / valid) : NAN; }
};

// The full band includes both the charged reading and the drop after unplugging
// USB. These display endpoints are not a capacity curve or power cutoff,
// and do not change the charger's nominal 4.2 V target.
constexpr float EMPTY_V = 3.300f;
constexpr float FULL_FROM_V = 4.180f;
constexpr const char *ESTIMATE = "linear-3300-4180mv-v3";
inline int8_t percentage(float battery_v) {
  if (!std::isfinite(battery_v) || battery_v < 2.0f || battery_v > 4.5f) return -1;
  return static_cast<int8_t>(std::lround(std::clamp((battery_v - EMPTY_V) / (FULL_FROM_V - EMPTY_V), 0.0f, 1.0f) * 100));
}
inline int8_t display_level(bool installed, const Window *window, uint64_t now) {
  if (!installed) return -2; // Configured absent, not automatically detected.
  if (!window || !window->valid || now < window->end_ms || now - window->end_ms > 30000) return -1;
  return percentage(2 * window->mean());
}
inline bool payload(char (&out)[512], const char *device, const char *boot,
                    bool installed, const Window &w, uint64_t now) {
  char volts[96] = "null,\"mean_mv\":null,\"max_mv\":null";
  if (w.valid) {
    if (!std::isfinite(w.mean()) || w.maximum > 3.3f) return false;
    std::snprintf(volts, sizeof(volts), "%ld,\"mean_mv\":%ld,\"max_mv\":%ld",
      std::lround(2000*w.minimum), std::lround(2000*w.mean()), std::lround(2000*w.maximum));
  }
  char percent[5] = "null";
  const auto level = display_level(installed, &w, now);
  if (level >= 0) std::snprintf(percent, sizeof(percent), "%u", static_cast<unsigned>(level));
  // device is ESPHome's validated node name; boot is locally generated hex.
  const int n = std::snprintf(out, sizeof(out),
    "{\"version\":2,\"device\":\"%s\",\"boot_id\":\"%s\",\"installed\":%s,"
    "\"start_ms\":%llu,\"end_ms\":%llu,\"sample_age_ms\":%llu,\"valid\":%u,\"invalid\":%u,"
    "\"min_mv\":%s,\"percent\":%s,\"estimate\":\"%s\"}", device, boot, installed ? "true" : "false",
    static_cast<unsigned long long>(w.start_ms), static_cast<unsigned long long>(w.end_ms),
    static_cast<unsigned long long>(now - w.end_ms), static_cast<unsigned>(w.valid),
    static_cast<unsigned>(w.invalid), volts, percent, ESTIMATE);
  return n > 0 && static_cast<size_t>(n) < sizeof(out);
}

class Diagnostics {
 public:
  static constexpr uint64_t WINDOW_MS = 10000;
  static constexpr size_t CAPACITY = 12; // About two minutes, RAM only.

  // Returns true when a completed window is available as the newest history row.
  bool record(float adc_v, uint64_t now) {
    bool completed = false;
    if (active_ && now - current_.start_ms >= WINDOW_MS) {
      history_[next_] = current_;
      next_ = (next_ + 1) % CAPACITY;
      count_ = std::min(count_ + 1, CAPACITY);
      current_ = {};
      active_ = false;
      completed = true;
    }
    if (!active_) { current_.start_ms = now; active_ = true; }
    current_.end_ms = now;
    // Preserve even implausible battery voltages for diagnosis; reject only
    // failed/nonphysical ADC readings. Do not treat failure as an empty connector.
    if (!std::isfinite(adc_v) || adc_v < 0) {
      ++current_.invalid;
    } else {
      if (!current_.valid) current_.minimum = current_.maximum = current_.first = adc_v;
      current_.minimum = std::min(current_.minimum, adc_v);
      current_.maximum = std::max(current_.maximum, adc_v);
      current_.last = adc_v;
      current_.sum += adc_v;
      ++current_.valid;
    }
    return completed;
  }
  size_t size() const { return count_; }
  const Window &at(size_t index) const { return history_[(next_ + CAPACITY - count_ + index) % CAPACITY]; }
  const Window *current() const { return active_ ? &current_ : nullptr; }

 private:
  Window current_, history_[CAPACITY];
  size_t next_ = 0, count_ = 0;
  bool active_ = false;
};
static_assert(sizeof(Diagnostics) <= 1024, "Battery diagnostics must stay within 1 KiB of RAM");
} // namespace meter::battery
