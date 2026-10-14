#include "firmware/components/meter_network/battery_diagnostics.h"
#include <cassert>
#include <iostream>
#include <limits>
#include <cstring>

using meter::battery::Diagnostics;
static bool near(float a, float b) { return std::fabs(a-b) < 0.0001f; }

int main() {
  Diagnostics d;
  assert(d.size() == 0 && d.current() == nullptr);
  // A brief dip must survive aggregation even when most readings are high.
  for (uint64_t i = 0; i < 100; ++i) assert(!d.record(i == 50 ? 1.5f : 2.1f, i*100));
  assert(d.record(1.9f, 10000));
  const auto &pulse = d.at(0);
  assert(pulse.start_ms == 0 && pulse.end_ms == 9900 && pulse.valid == 100 && !pulse.invalid);
  assert(near(pulse.minimum, 1.5f) && near(pulse.maximum, 2.1f) && near(pulse.mean(), 2.094f));
  assert(near(d.current()->mean(), 1.9f) && d.current()->valid == 1);

  // Invalid readings remain explicit; zero is data, not a presence decision.
  assert(d.record(NAN, 20000));
  assert(!d.record(std::numeric_limits<float>::infinity(), 20100));
  assert(!d.record(-1.0f, 20200));
  assert(d.record(0, 30000));
  const auto &bad = d.at(2);
  assert(!bad.valid && bad.invalid == 3 && std::isnan(bad.mean()) && std::isnan(bad.minimum));
  assert(!d.record(NAN, 30100));
  assert(!d.record(2.0f, 30200));
  assert(d.record(2.1f, 40000));
  const auto &mixed = d.at(3);
  assert(mixed.valid == 2 && mixed.invalid == 1 && near(mixed.mean(), 1.0f));
  assert(near(mixed.minimum, 0) && near(mixed.maximum, 2) && near(mixed.last-mixed.first, 2));

  // No USB connection is required for storage. Replay stays ordered after wrap.
  Diagnostics history;
  for (uint64_t i = 0; i <= 30; ++i) history.record(1.8f + i/100.0f, i*10000);
  assert(history.size() == Diagnostics::CAPACITY);
  for (size_t i = 0; i < history.size(); ++i) {
    const auto &w = history.at(i);
    assert(w.start_ms == (18+i)*10000 && w.end_ms == w.start_ms && w.valid == 1);
    assert(near(w.mean(), 1.8f+(18+i)/100.0f));
  }
  assert(history.current()->start_ms == 300000);
  // Long uptime and a scheduling gap neither wrap timestamps nor invent samples.
  const uint64_t late = (1ULL << 32) + 1000;
  assert(history.record(1.7f, late));
  assert(history.at(11).end_ms == 300000 && history.current()->start_ms == late);
  assert(history.record(1.6f, late+10000));
  assert(history.at(11).start_ms == late && history.at(11).valid == 1);
  assert(near(history.current()->mean(), 1.6f));
  using meter::battery::percentage;
  using meter::battery::display_level;
  assert(percentage(2.5f)==0 && percentage(3.0f)==0 && percentage(3.3f)==0);
  assert(percentage(3.740f)==50 && percentage(4.170f)==99);
  // Charged and newly unplugged readings share the full band.
  for (float v : {4.180f, 4.182f, 4.188f, 4.194f, 4.200f, 4.236f}) assert(percentage(v)==100);
  assert(percentage(4.4f)==100);
  assert(percentage(NAN)==-1 && percentage(0)==-1 && percentage(5)==-1);
  assert(display_level(false,nullptr,0)==-2 && display_level(true,nullptr,0)==-1);
  assert(display_level(true,&bad,40000)==-1);
  assert(display_level(true,&pulse,40000)==-1); // Old data must not claim a current level.
  assert(display_level(true,&pulse,10000)>=0);
  char payload[512];
  assert(meter::battery::payload(payload,"cyd-meter-1","0123456789abcdef",true,pulse,10000));
  assert(std::strstr(payload,"\"min_mv\":3000") && std::strstr(payload,"\"mean_mv\":4188"));
  assert(std::strstr(payload,"\"percent\":100") && std::strstr(payload,"\"estimate\":\"linear-3300-4180mv-v3\""));
  assert(meter::battery::payload(payload,"cyd-meter-1","0123456789abcdef",true,bad,30000));
  assert(std::strstr(payload,"\"min_mv\":null") && std::strstr(payload,"\"percent\":null"));
  // Metadata must fit the existing request cap even at protocol bounds.
  meter::battery::Window bounds;
  bounds.start_ms = 9007199254730991ULL; bounds.end_ms = bounds.start_ms + 9900;
  bounds.valid = 1000; bounds.invalid = 1000;
  bounds.minimum = bounds.maximum = 2.118f; bounds.sum = 2118.0;
  const char longest_device[] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
  assert(meter::battery::payload(payload,longest_device,"0123456789abcdef",true,bounds,bounds.end_ms+30000));
  assert(std::strlen(payload)<512 && std::strstr(payload,"\"percent\":100"));
  assert(meter::battery::payload(payload,"cyd-meter-1","0123456789abcdef",false,bounds,bounds.end_ms));
  assert(std::strstr(payload,"\"percent\":null"));
  std::cout << "battery diagnostics checks passed; RAM bytes=" << sizeof(Diagnostics) << '\n';
}
