#pragma once
#include <algorithm>
#include <cstdint>

// Coordinates here are relative to the original 320x240 landscape view.
// The requested bottom-edge order is bottom, right, top, left.
namespace meter::orientation {
struct Point { int x, y; };
struct Rect {
  int x, y, width, height;
  constexpr bool contains(Point p) const {
    return p.x >= x && p.y >= y && p.x < x + width && p.y < y + height;
  }
};
constexpr int width(uint8_t position) { return position & 1 ? 240 : 320; }
constexpr int height(uint8_t position) { return position & 1 ? 320 : 240; }
constexpr int degrees(uint8_t position) { return (450 - 90 * position) % 360; }

inline Point to_view(Point p, uint8_t position) {
  if (p.x < 0 || p.x >= 320 || p.y < 0 || p.y >= 240 || position > 3) return {-1, -1};
  switch (position) {
    case 1: return {239 - p.y, p.x};
    case 2: return {319 - p.x, 239 - p.y};
    case 3: return {p.y, 319 - p.x};
    default: return p;
  }
}

struct Calibration {
  int x_min = 280, x_max = 3860, y_min = 340, y_max = 3860;
  bool swap_xy = false, mirror_x = false, mirror_y = false;
  Point normalize(int x, int y) const {
    if (x < 0 || x > 4095 || y < 0 || y > 4095 || x_max - x_min < 10 || y_max - y_min < 10)
      return {-1, -1};
    if (swap_xy) std::swap(x, y);
    x = (std::clamp(x, x_min, x_max) - x_min) * 319 / (x_max - x_min);
    y = (std::clamp(y, y_min, y_max) - y_min) * 239 / (y_max - y_min);
    return {mirror_x ? 319 - x : x, mirror_y ? 239 - y : y};
  }
};

} // namespace meter::orientation
