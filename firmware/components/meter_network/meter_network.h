#pragma once
#include "esphome/core/component.h"
#include "esphome/components/wifi/wifi_component.h"
#include "esphome/components/display/display.h"
#include "esphome/core/preferences.h"
#include "model.h"
#include "http.h"
#include "ui.h"
#include "battery_diagnostics.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include <string>
#include <atomic>
#include "esp_event.h"
#include "esp_wifi.h"
#include "esp_system.h"

namespace esphome::meter_network {
class MeterNetwork : public Component {
 public:
  void configure(const char *host, uint16_t port, const char *token, uint32_t poll, uint32_t timeout) {
    host_ = host; port_ = port; token_ = token; poll_ms_ = poll; timeout_ms_ = timeout;
  }
  void setup() override;
  void loop() override;
  void dump_config() override;
  const meter::State &state() const { return state_; }
  bool prepare_display();
  void draw(display::Display &display);
  void display_updated(uint32_t duration_ms);
  void set_display(display::Display *value) { display_ = value; }
  void set_touch_calibration(int x_min, int x_max, int y_min, int y_max, bool swap, bool mirror_x, bool mirror_y) {
    calibration_ = {x_min, x_max, y_min, y_max, swap, mirror_x, mirror_y};
  }
  void touch_press(int x_raw, int y_raw);
  void touch_move(int x_raw, int y_raw);
  void touch_release();
  void record_battery_voltage(float adc_v);
  void dump_battery_history();
  void configure_battery(bool installed, bool telemetry, const char *device) {
    battery_installed_ = installed; battery_telemetry_ = telemetry; device_ = device;
  }
 protected:
  struct Job {
    bool battery = false;
    meter::battery::Window window;
  };
  struct Result {
    meter::Model model;
    const char *error = "transport";
    bool valid = false;
    uint32_t duration = 0;
    uint32_t stack_free = 0;
    bool battery = false;
  };
  std::string host_, token_;
  uint16_t port_ = 8080;
  uint32_t poll_ms_ = 60000, timeout_ms_ = 10000, backoff_ms_ = 60000;
  uint64_t next_poll_ = 0, next_log_ = 0;
  QueueHandle_t results_ = nullptr;
  QueueHandle_t jobs_ = nullptr;
  TaskHandle_t worker_ = nullptr;
  bool busy_ = false;
  meter::State state_;
  meter::battery::Diagnostics battery_diagnostics_;
  bool battery_installed_ = false, battery_telemetry_ = false;
  std::string device_;
  char boot_id_[17]{};
  uint64_t attempted_battery_window_ = 0;
  const char *last_error_ = "starting";
  esp_event_handler_instance_t wifi_handler_ = nullptr;
  std::atomic<uint16_t> wifi_reason_{0};
  meter::ui::Frame frame_;
  bool frame_ready_ = false;
  display::Display *display_ = nullptr;
  meter::orientation::Calibration calibration_;
  meter::settings::Tap tap_;
  meter::settings::Controller settings_;
  ESPPreferenceObject settings_preference_;
  uint8_t frame_position_ = 1;
  meter::settings::Action pending_action_ = meter::settings::Action::none;
  uint64_t next_display_log_ = 0;
  static void wifi_event(void *argument, esp_event_base_t base, int32_t event, void *data);
  static void worker(void *argument);
  void apply_settings_();
};
} // namespace esphome::meter_network
