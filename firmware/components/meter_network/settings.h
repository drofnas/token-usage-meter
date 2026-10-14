#pragma once
#include "orientation.h"

namespace meter::settings {
enum class Action : uint8_t { none, open, back, claude, claude_five, codex, codex_five, rotate0, rotate1, rotate2, rotate3 };
struct Values {
  uint8_t position=1;
  uint8_t flags=15; // Claude, Claude 5h, Codex, Codex 5h.
  bool enabled(size_t provider)const { return flags & (1U << (provider*2)); }
  bool five_hour(size_t provider)const { return flags & (2U << (provider*2)); }
  uint32_t encode()const { return 0x43590000U | (static_cast<uint32_t>(flags)<<8) | position; }
  bool decode(uint32_t value) {
    if((value&0xfffff0fcU)!=0x43590000U)return false;
    position=value&3;flags=(value>>8)&15;return true;
  }
};
struct Control { Action action; orientation::Rect rect; };
// Drawing and hit testing share these coordinates, including rotated layouts.
inline Control control(Action action,uint8_t position) {
  const int width=orientation::width(position);
  const bool portrait=position&1;
  switch(action) {
    case Action::open:return {action,{width-40,0,40,40}};
    case Action::back:return {action,{0,0,40,portrait?40:35}};
    case Action::claude:return {action,{8,portrait?53:35,width-16,portrait?40:35}};
    case Action::claude_five:return {action,{16,portrait?93:70,width-24,36}};
    case Action::codex:return {action,{8,portrait?151:111,width-16,portrait?40:35}};
    case Action::codex_five:return {action,{16,portrait?191:146,width-24,36}};
    default: {
      int index=static_cast<int>(action)-static_cast<int>(Action::rotate0);
      if(index<0||index>3)return {Action::none,{0,0,0,0}};
      const int start=portrait?12:82, gap=5, size=(width-start-12-3*gap)/4;
      return {action,{start+index*(size+gap),portrait?269:195,size,portrait?39:34}};
    }
  }
}
inline Action hit(orientation::Point point,bool menu,const Values &values) {
  if(!menu)return control(Action::open,values.position).rect.contains(point)?Action::open:Action::none;
  for(Action a:{Action::back,Action::claude,Action::claude_five,Action::codex,Action::codex_five,
                Action::rotate0,Action::rotate1,Action::rotate2,Action::rotate3}) {
    if(a==Action::claude_five&&!values.enabled(0))continue;
    if(a==Action::codex_five&&!values.enabled(1))continue;
    if(control(a,values.position).rect.contains(point))return a;
  }
  return Action::none;
}
class Controller {
 public:
  Values values;
  bool menu=false,save_failed=false;
  template<class Store> bool restore(Store &store) {
    uint32_t saved=0;values=Values{};
    return store.load(saved)&&values.decode(saved);
  }
  template<class Store> bool act(Action action,Store &store) {
    if(action==Action::open) { menu=true;return true; }
    if(action==Action::back) { menu=false;return true; }
    const auto previous=values.encode();
    switch(action) {
      case Action::claude:values.flags^=1;break;
      case Action::claude_five:if(values.enabled(0))values.flags^=2;break;
      case Action::codex:values.flags^=4;break;
      case Action::codex_five:if(values.enabled(1))values.flags^=8;break;
      case Action::rotate0:case Action::rotate1:case Action::rotate2:case Action::rotate3:
        values.position=static_cast<uint8_t>(action)-static_cast<uint8_t>(Action::rotate0);break;
      default:return false;
    }
    if(previous==values.encode())return false;
    save_failed=!(store.save(values.encode())&&store.sync());return true;
  }
};
class Tap {
 public:
  void press(Action action,uint64_t now) {
    if(active_)return;
    active_=true;action_=now>=ready_after_?action:Action::none;pressed_at_=now;
  }
  void move(Action action) { if(active_&&action!=action_)action_=Action::none; }
  Action release(uint64_t now) {
    if(!active_)return Action::none;
    const auto result=now>=pressed_at_&&now-pressed_at_>=40?action_:Action::none;
    active_=false;action_=Action::none;ready_after_=now+180;return result;
  }
 private:
  bool active_=false;
  Action action_=Action::none;
  uint64_t pressed_at_=0,ready_after_=0;
};
} // namespace meter::settings
