#pragma once
#include <algorithm>
#include <cmath>
#include <cstdint>
#include <cstdlib>
#include <cstring>

namespace meter {
constexpr size_t MAX_BODY=4096;
constexpr int64_t MAX_EPOCH=4102444800LL;
constexpr int64_t DAY=86400;
inline bool eq(const char *a,const char *b) { return std::strcmp(a,b)==0; }
struct Window { bool known=false; double remaining=0; int64_t reset=0; };
struct Provider {
  char scope[65]{}, error[24]{};
  int64_t observed=0,resets=-1;
  Window five_hour,weekly;
};
struct Model {
  int64_t updated=0,as_of=0,stale_after=900;
  Provider providers[2]; // Claude, Codex.
};
inline bool same_window(const Window &a,const Window &b) {
  return a.known==b.known && a.remaining==b.remaining && a.reset==b.reset;
}
class Decoder {
 public:
  explicit Decoder(const char *input):p_(input) {}
  bool decode(Model &m) {
    static const char *const fields[]={"version","updated_at","as_of","stale_after_seconds","claude","codex"};
    if(!take('{'))return false;
    uint32_t seen=0;
    for(int i=0;i<6;++i) {
      const int k=key(fields,seen); int64_t version=0; bool ok=false;
      if(k==0)ok=integer(version,3,3);
      if(k==1)ok=integer(m.updated,1,MAX_EPOCH);
      if(k==2)ok=integer(m.as_of,1,MAX_EPOCH);
      if(k==3)ok=integer(m.stale_after,30,3600);
      if(k==4||k==5)ok=provider(m.providers[k-4]);
      if(!ok||!take(i==5?'}':','))return false;
    }
    ws();return *p_==0;
  }
 private:
  const char *p_;
  void ws() { while (*p_ == ' ' || *p_ == '\t' || *p_ == '\n' || *p_ == '\r') ++p_; }
  bool take(char c) { ws(); if (*p_ != c) return false; ++p_; return true; }
  bool null() { ws(); if (std::strncmp(p_, "null", 4) != 0) return false; p_ += 4; return true; }
  template<size_t N> bool string(char (&out)[N], bool nullable = false) {
    if (nullable && null()) { out[0] = 0; return true; }
    if (!take('"')) return false;
    size_t n = 0;
    while (*p_ != '"') {
      unsigned char c = *p_;
      if (c < 32 || c > 126) return false; // All defined contract strings are ASCII.
      ++p_;
      if (c == '\\') {
        c = *p_++; if (c == 0) return false;
        if (c == 'u') {
          unsigned value = 0;
          for (int i = 0; i < 4; ++i) {
            const unsigned char digit = *p_;
            int h = digit >= '0' && digit <= '9' ? digit - '0' :
                    digit >= 'a' && digit <= 'f' ? digit - 'a' + 10 :
                    digit >= 'A' && digit <= 'F' ? digit - 'A' + 10 : -1;
            if (h < 0) return false;
            ++p_; value = value * 16 + h;
          }
          if (value < 32 || value > 126) return false;
          c = static_cast<unsigned char>(value);
        } else if (c != '"' && c != '\\' && c != '/') return false;
      }
      if (n + 1 >= N) return false;
      out[n++] = static_cast<char>(c);
    }
    ++p_; out[n] = 0; return true;
  }
  bool number(double &out, double min, double max, bool nullable = false) {
    if (nullable && null()) { out = -1; return true; }
    ws(); const char *start = p_;
    if (*p_ == '-') ++p_;
    if (*p_ == '0') ++p_;
    else { if (*p_ < '1' || *p_ > '9') return false; while (*p_ >= '0' && *p_ <= '9') ++p_; }
    if (*p_ == '.') { ++p_; if (*p_ < '0' || *p_ > '9') return false; while (*p_ >= '0' && *p_ <= '9') ++p_; }
    if (*p_ == 'e' || *p_ == 'E') {
      ++p_; if (*p_ == '+' || *p_ == '-') ++p_;
      if (*p_ < '0' || *p_ > '9') return false;
      while (*p_ >= '0' && *p_ <= '9') ++p_;
    }
    char *end = nullptr; out = std::strtod(start, &end);
    return end == p_ && std::isfinite(out) && out >= min && out <= max;
  }
  bool integer(int64_t &out, int64_t min, int64_t max, bool nullable = false) {
    double value;
    if (!number(value, min, max, nullable) || std::floor(value) != value) return false;
    out = static_cast<int64_t>(value); return true;
  }
  template<size_t N> int key(const char *const (&fields)[N], uint32_t &seen) {
    char name[24]{}; if (!string(name) || !take(':')) return -1;
    for (size_t i = 0; i < N; ++i) if (eq(name, fields[i])) {
      if (seen & (1U << i)) return -1;
      seen |= 1U << i; return static_cast<int>(i);
    }
    return -1;
  }
  bool window(Window &w) {
    if(null())return true;
    static const char *const fields[]={"remaining_percent","reset_at"};
    if(!take('{'))return false;
    uint32_t seen=0;
    for(int i=0;i<2;++i) {
      const int k=key(fields,seen);
      if(k<0 || !(k==0?number(w.remaining,0,100):integer(w.reset,0,MAX_EPOCH)) || !take(i==1?'}':','))return false;
    }
    w.known=true;return true;
  }
  bool provider(Provider &v) {
    static const char *const fields[]={"scope","observed_at","source_error","five_hour","weekly","resets_available"};
    if(!take('{'))return false;
    uint32_t seen=0;
    for(int i=0;i<6;++i) {
      const int k=key(fields,seen);bool ok=false;
      if(k==0)ok=string(v.scope);
      if(k==1)ok=integer(v.observed,0,MAX_EPOCH);
      if(k==2)ok=string(v.error);
      if(k==3)ok=window(v.five_hour);
      if(k==4)ok=window(v.weekly);
      if(k==5)ok=integer(v.resets,0,2147483647,true);
      if(!ok)return false;
      if(take('}'))return (seen&31)==31;
      if(!take(','))return false;
    }
    return false;
  }
};
inline bool semantics(const Model &m) {
  for(const auto &p:m.providers) {
    if(!(eq(p.error,"")||eq(p.error,"auth_missing")||eq(p.error,"auth_failed")||eq(p.error,"source_timeout")||
         eq(p.error,"source_unavailable")||eq(p.error,"source_invalid")||eq(p.error,"rate_limited")))return false;
    if(!p.observed) {
      if(p.scope[0]||p.five_hour.known||p.weekly.known||p.resets!=-1)return false;
    } else {
      if(p.observed>m.updated||std::strlen(p.scope)!=64)return false;
      for(char c:p.scope)if(c&&!((c>='0'&&c<='9')||(c>='a'&&c<='f')))return false;
    }
  }
  return true;
}
inline bool decode(const char *raw,size_t size,Model &out) {
  if(size==0||size>MAX_BODY||std::memchr(raw,0,size)||raw[size]!=0)return false;
  Model candidate;
  if(!Decoder(raw).decode(candidate)||!semantics(candidate))return false;
  out=candidate;return true;
}
struct State {
  Model model;
  bool accepted=false,transport_failed=true;
  uint64_t accepted_ms=0,as_of_base_ms=0;
  uint64_t as_of(uint64_t now)const { return (as_of_base_ms+now-accepted_ms)/1000; }
  bool stale(size_t index,uint64_t now)const {
    const auto &p=model.providers[index];const auto clock=static_cast<int64_t>(as_of(now));
    return transport_failed||p.error[0]||clock<p.observed||clock-p.observed>=model.stale_after||clock<model.updated;
  }
  bool accept(const Model &next,uint64_t now) {
    if(accepted&&next.updated<model.updated) { transport_failed=true;return false; }
    auto candidate=next;
    for(size_t i=0;i<2;++i) {
      const auto &old=model.providers[i];auto &p=candidate.providers[i];
      if(!accepted||!old.observed)continue;
      if(!p.observed) { p=old;std::strcpy(p.error,"source_unavailable");continue; }
      if(eq(old.scope,p.scope)&&(p.observed<old.observed || (p.observed==old.observed&&
          (!same_window(old.five_hour,p.five_hour)||!same_window(old.weekly,p.weekly)||old.resets!=p.resets)))) {
        p=old;std::strcpy(p.error,"source_invalid");
      }
    }
    const uint64_t clock=static_cast<uint64_t>(next.as_of)*1000;
    as_of_base_ms=accepted?std::max(clock,as_of_base_ms+now-accepted_ms):clock;
    model=candidate;accepted=true;transport_failed=false;accepted_ms=now;return true;
  }
};
} // namespace meter
