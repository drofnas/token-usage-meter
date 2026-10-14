#include "meter_network.h"
#include "esphome/core/log.h"
#include "esp_system.h"
#include "esp_random.h"

namespace esphome::meter_network {
static const char *const TAG = "meter_network";
namespace {
// Stable CYD1 key; versioned settings never collide with the previous app rotation key.
constexpr uint32_t SETTINGS_KEY = 0x43594431;
struct SettingsStore {
  ESPPreferenceObject &preference;
  bool load(uint32_t &value) { return preference.load(&value); }
  bool save(uint32_t value) { return preference.save(&value); }
  bool sync() { return global_preferences && global_preferences->sync(); }
};
void log_battery_window(const meter::battery::Window &w, const char *kind) {
  ESP_LOGI("meter_battery", "%s start_ms=%llu end_ms=%llu valid=%u invalid=%u adc_v=%.3f/%.3f/%.3f bat_v=%.3f/%.3f/%.3f delta_v=%+.3f",
    kind, static_cast<unsigned long long>(w.start_ms), static_cast<unsigned long long>(w.end_ms),
    static_cast<unsigned>(w.valid), static_cast<unsigned>(w.invalid),
    w.minimum, w.mean(), w.maximum, 2*w.minimum, 2*w.mean(), 2*w.maximum, 2*(w.last-w.first));
}
}
void MeterNetwork::setup() {
  if (global_preferences) settings_preference_ = global_preferences->make_preference<uint32_t>(SETTINGS_KEY, true);
  SettingsStore store{settings_preference_};
  const bool restored = settings_.restore(store);
  if (display_) display_->set_rotation(static_cast<display::DisplayRotation>(meter::orientation::degrees(settings_.values.position)));
  ESP_LOGI("meter_rotation", "position=%u restored=%d", settings_.values.position, restored);
  backoff_ms_ = poll_ms_;
  std::snprintf(boot_id_, sizeof(boot_id_), "%08x%08x", static_cast<unsigned>(esp_random()), static_cast<unsigned>(esp_random()));
  results_ = xQueueCreate(1, sizeof(Result));
  jobs_ = xQueueCreate(1, sizeof(Job));
  if (!results_ || !jobs_ || xTaskCreate(worker, "meter_http", 16384, this, 1, &worker_) != pdPASS) {
    if (results_) { vQueueDelete(results_); results_ = nullptr; }
    if (jobs_) { vQueueDelete(jobs_); jobs_ = nullptr; }
    last_error_ = "resources"; mark_failed();
  }
}
void MeterNetwork::dump_config() {
  ESP_LOGCONFIG(TAG, "Private meter client: poll=%ums timeout=%ums cap=4096 bytes", static_cast<unsigned>(poll_ms_), static_cast<unsigned>(timeout_ms_));
  ESP_LOGCONFIG("meter_battery", "installed_setting=%d telemetry=%d estimate=%s empty_v=%.3f full_from_v=%.3f charging=unknown", battery_installed_, battery_telemetry_, meter::battery::ESTIMATE, meter::battery::EMPTY_V, meter::battery::FULL_FROM_V);
}
void MeterNetwork::record_battery_voltage(float adc_v) {
  if (battery_diagnostics_.record(adc_v, meter::monotonic_ms()))
    log_battery_window(battery_diagnostics_.at(battery_diagnostics_.size()-1), "live");
}
void MeterNetwork::dump_battery_history() {
  ESP_LOGI("meter_battery", "history begin: GPIO34 divider=2 min/mean/max; installed_setting=%d charging=unknown; timestamps are boot-relative", battery_installed_);
  for (size_t i = 0; i < battery_diagnostics_.size(); ++i)
    log_battery_window(battery_diagnostics_.at(i), "history");
  if (const auto *current = battery_diagnostics_.current()) log_battery_window(*current, "partial");
  ESP_LOGI("meter_battery", "history end");
}
void MeterNetwork::worker(void *argument) {
  auto *self = static_cast<MeterNetwork *>(argument);
  for (;;) {
    Job job;
    if (xQueueReceive(self->jobs_, &job, portMAX_DELAY) != pdTRUE) continue;
    uint64_t start = meter::monotonic_ms();
    Result result;
    result.battery = job.battery;
    {
      meter::Http http;
      if (job.battery) {
        char payload[512];
        if (meter::battery::payload(payload, self->device_.c_str(), self->boot_id_, self->battery_installed_, job.window, start)) {
          result.valid = http.post_battery(self->host_.c_str(), self->port_, self->token_.c_str(), self->timeout_ms_, payload);
          result.error = result.valid ? "acknowledgement" : http.error;
          result.valid = result.valid && meter::eq(http.body, "{\"version\":2,\"status\":\"stored\"}");
          if (result.valid) result.error = "none";
        } else result.error = "battery_sample";
      } else if (http.get(self->host_.c_str(), self->port_, self->token_.c_str(), self->timeout_ms_)) {
        result.valid = meter::decode(http.body, http.size, result.model);
        result.error = result.valid ? "none" : "payload";
        if (result.valid && result.model.stale_after * 1000 < 2 * self->poll_ms_) {
          result.valid = false; result.error = "threshold";
        }
      } else result.error = http.error;
    } // Close socket before publishing and sleeping; main loop owns all state/logs.
    result.duration = meter::monotonic_ms() - start;
    result.stack_free = uxTaskGetStackHighWaterMark(nullptr);
    xQueueOverwrite(self->results_, &result);
  }
}
void MeterNetwork::wifi_event(void *argument, esp_event_base_t, int32_t, void *data) {
  auto *self = static_cast<MeterNetwork *>(argument);
  auto *event = static_cast<wifi_event_sta_disconnected_t *>(data);
  self->wifi_reason_.store(event->reason, std::memory_order_relaxed);
}
void MeterNetwork::loop() {
  // Touch callbacks only queue this action. All pages finish before the main
  // loop changes panel geometry and renders the next frozen frame.
  if (pending_action_ != meter::settings::Action::none) apply_settings_();
  if (!results_) return;
  if (!wifi_handler_) {
    // Register after all setup phases, once ESPHome has created the event loop.
    esp_event_handler_instance_register(WIFI_EVENT, WIFI_EVENT_STA_DISCONNECTED,
                                       wifi_event, this, &wifi_handler_);
  }
  uint64_t now = meter::monotonic_ms();
  Result result;
  if (xQueueReceive(results_, &result, 0) == pdTRUE) {
    busy_ = false;
    if (result.battery) {
      ESP_LOGI("meter_battery", "upload stored=%d error=%s duration_ms=%u", result.valid, result.error, static_cast<unsigned>(result.duration));
    } else {
      bool accepted = result.valid && state_.accept(result.model, now);
      if (!accepted) state_.transport_failed = true;
      last_error_ = accepted ? "none" : result.valid ? "ordering" : result.error;
      next_poll_ = now + (accepted ? poll_ms_ : backoff_ms_);
      backoff_ms_ = accepted ? poll_ms_ : std::min<uint32_t>(300000, backoff_ms_ * 2);
      ESP_LOGI(TAG, "poll accepted=%d error=%s duration_ms=%u heap=%u stack_free=%u", accepted,
               last_error_, static_cast<unsigned>(result.duration), static_cast<unsigned>(esp_get_free_heap_size()), static_cast<unsigned>(result.stack_free));
    }
  }
  bool connected = wifi::global_wifi_component->is_connected();
  if (!connected) { state_.transport_failed = true; last_error_ = "wifi"; }
  if (connected && !busy_) {
    Job job;
    if (now >= next_poll_) {
      busy_ = xQueueSend(jobs_, &job, 0) == pdTRUE;
    } else if (battery_telemetry_ && battery_diagnostics_.size()) {
      const auto &window = battery_diagnostics_.at(battery_diagnostics_.size()-1);
      // Latest window only: outages leave explicit gaps, no flash writes or backlog.
      if (window.end_ms != attempted_battery_window_ && now - window.end_ms <= 30000) {
        job.battery = true; job.window = window;
        busy_ = xQueueSend(jobs_, &job, 0) == pdTRUE;
        if (busy_) attempted_battery_window_ = window.end_ms;
      }
    }
  }
  if (now >= next_log_) {
    next_log_ = now + 10000;
    ESP_LOGI(TAG, "error=%s claude_stale=%d codex_stale=%d busy=%d heap=%u uptime=%llu wifi_reason=%u",
      last_error_, state_.stale(0,now), state_.stale(1,now), busy_,
      static_cast<unsigned>(esp_get_free_heap_size()), static_cast<unsigned long long>(now/1000),
      connected ? 0 : wifi_reason_.load(std::memory_order_relaxed));
  }
}
bool MeterNetwork::prepare_display() {
  const auto now = meter::monotonic_ms();
  auto next = meter::ui::project(state_, now, settings_);
  const auto *window = battery_diagnostics_.size() ? &battery_diagnostics_.at(battery_diagnostics_.size()-1) : nullptr;
  next.battery = meter::battery::display_level(battery_installed_, window, now);
  if (frame_ready_ && frame_position_ == settings_.values.position && meter::ui::equal(frame_, next)) return false;
  frame_ = next; frame_ready_ = true;
  frame_position_ = settings_.values.position;
  return true;
}
void MeterNetwork::draw(display::Display &display) {
  struct Canvas {
    display::Display &target;
    void rect(int x,int y,int w,int h,uint32_t rgb) {
      const Color color(rgb>>16,(rgb>>8)&255,rgb&255);
      if(x==0 && y==0 && w==target.get_width() && h==target.get_height()) target.fill(color);
      else target.filled_rectangle(x,y,w,h,color);
    }
  } canvas{display};
  // ESPHome replays this same frozen frame for all four buffer pages.
  meter::ui::draw(canvas, frame_, frame_position_);
}
void MeterNetwork::touch_press(int x_raw, int y_raw) {
  const auto point = meter::orientation::to_view(calibration_.normalize(x_raw,y_raw),settings_.values.position);
  tap_.press(meter::settings::hit(point,settings_.menu,settings_.values),meter::monotonic_ms());
}
void MeterNetwork::touch_move(int x_raw,int y_raw) {
  const auto point=meter::orientation::to_view(calibration_.normalize(x_raw,y_raw),settings_.values.position);
  tap_.move(meter::settings::hit(point,settings_.menu,settings_.values));
}
void MeterNetwork::touch_release() {
  const auto action=tap_.release(meter::monotonic_ms());
  if(action!=meter::settings::Action::none)pending_action_=action;
}
void MeterNetwork::apply_settings_() {
  const auto action=pending_action_;
  pending_action_=meter::settings::Action::none;
  SettingsStore store{settings_preference_};
  if(!settings_.act(action,store))return;
  if(settings_.save_failed)ESP_LOGW("meter_settings","Settings save failed; current selection may not survive restart");
  if(display_) {
    display_->set_rotation(static_cast<display::DisplayRotation>(meter::orientation::degrees(settings_.values.position)));
    if(prepare_display()) {
      const auto start=meter::monotonic_ms();
      display_->update();
      display_updated(meter::monotonic_ms()-start);
    }
  }
}
void MeterNetwork::display_updated(uint32_t duration_ms) {
  const uint64_t now=meter::monotonic_ms();
  if(now<next_display_log_)return;
  next_display_log_=now+10000;
  ESP_LOGI("meter_display","portrait=%d settings=%d duration_ms=%u heap=%u",
    (frame_position_&1)!=0,frame_.menu,static_cast<unsigned>(duration_ms),static_cast<unsigned>(esp_get_free_heap_size()));
}
} // namespace esphome::meter_network
