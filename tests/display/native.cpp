#include "firmware/components/meter_network/ui.h"
#include <cassert>
#include <fstream>
#include <iostream>
#include <string>
#include <vector>
#include <filesystem>

struct Canvas {
  int width,height,clip_start=0,clip_end;
  std::vector<uint8_t> pixels;
  explicit Canvas(uint8_t position):width(meter::orientation::width(position)),height(meter::orientation::height(position)),clip_end(height),pixels(width*height*3) {}
  void rect(int x,int y,int w,int h,uint32_t rgb) {
    if(x<0||y<0||w<0||h<0||x+w>width||y+h>height) {
      std::cerr<<"Out of bounds: "<<x<<","<<y<<" "<<w<<"x"<<h<<" on "<<width<<"x"<<height<<"\n";
      std::abort();
    }
    for(int yy=std::max(y,clip_start);yy<std::min(y+h,clip_end);++yy)for(int xx=x;xx<x+w;++xx) {
      size_t i=(yy*width+xx)*3;
      pixels[i]=(rgb>>16)&0xE0;pixels[i+1]=(rgb>>8)&0xE0;pixels[i+2]=rgb&0xC0;
    }
  }
  void save(const std::filesystem::path &path)const {
    std::ofstream out(path,std::ios::binary);
    out<<"P6\n"<<width<<" "<<height<<"\n255\n";
    out.write(reinterpret_cast<const char *>(pixels.data()),pixels.size());
    assert(out.good());
  }
};
struct Store {
  uint32_t value=0;int writes=0,syncs=0;bool exists=false,fail=false;
  bool load(uint32_t &out) { out=value;return exists; }
  bool save(uint32_t next) { ++writes;if(fail)return false;value=next;exists=true;return true; }
  bool sync() { ++syncs;return !fail; }
};

void test_settings() {
  using namespace meter::settings;
  Store store;Controller ui;
  assert(!ui.restore(store)&&ui.values.position==1&&ui.values.flags==15);
  assert(ui.act(Action::open,store)&&ui.menu&&store.writes==0);
  assert(ui.act(Action::claude_five,store)&&!ui.values.five_hour(0));
  assert(ui.act(Action::codex,store)&&!ui.values.enabled(1));
  assert(!ui.act(Action::codex_five,store)); // Disabled subordinate control.
  assert(ui.act(Action::rotate3,store)&&ui.values.position==3);
  const int writes=store.writes;
  assert(!ui.act(Action::rotate3,store)&&store.writes==writes);
  Controller restarted;assert(restarted.restore(store));
  assert(restarted.values.encode()==ui.values.encode()&&!restarted.menu);
  assert(ui.act(Action::back,store)&&!ui.menu&&store.writes==writes);
  store.fail=true;
  assert(ui.act(Action::claude,store)&&ui.save_failed);
  for(uint32_t corrupt:{0U,1U,0x43590004U,0x43591000U,0xffffffffU}) {
    store.value=corrupt;Controller invalid;
    assert(!invalid.restore(store)&&invalid.values.position==1&&invalid.values.flags==15);
  }
  for(int position=0;position<4;++position)for(int flags=0;flags<16;++flags) {
    Values original;original.position=position;original.flags=flags;
    Values roundtrip;assert(roundtrip.decode(original.encode())&&roundtrip.encode()==original.encode());
  }
  Tap tap;
  tap.press(Action::open,0);assert(tap.release(39)==Action::none);
  tap.press(Action::open,300);assert(tap.release(350)==Action::open);
  tap.press(Action::open,400);assert(tap.release(450)==Action::none);
  tap.press(Action::claude,700);tap.move(Action::codex);tap.move(Action::claude);
  assert(tap.release(1000)==Action::none&&!static_cast<int>(tap.release(1100)));
  tap.press(Action::open,1400);assert(tap.release(5000)==Action::open); // Hold is one action.
  meter::orientation::Calibration calibration{280,3860,340,3860,true,false,false};
  auto p=calibration.normalize(340,280);assert(p.x==0&&p.y==0);
  p=calibration.normalize(3860,3860);assert(p.x==319&&p.y==239);
  // Check every native touch point maps in bounds and controls use the same view as drawing.
  for(int position=0;position<4;++position) {
    Values v;v.position=position;
    for(int y=0;y<240;++y)for(int x=0;x<320;++x) {
      auto q=meter::orientation::to_view({x,y},position);
      assert(q.x>=0&&q.y>=0&&q.x<meter::orientation::width(position)&&q.y<meter::orientation::height(position));
    }
    for(Action a:{Action::open,Action::back,Action::claude,Action::claude_five,Action::codex,Action::codex_five,
                  Action::rotate0,Action::rotate1,Action::rotate2,Action::rotate3}) {
      const auto r=control(a,position).rect;
      for(int y=r.y;y<r.y+r.height;++y)for(int x=r.x;x<r.x+r.width;++x)
        assert(hit({x,y},a!=Action::open,v)==a);
    }
    assert(hit({20,20},false,v)==Action::none); // The old title no longer rotates.
  }
}

void test_model(const std::string &raw,meter::Model &model) {
  assert(meter::decode(raw.c_str(),raw.size(),model));
  assert(model.providers[0].five_hour.remaining==72&&model.providers[1].weekly.remaining==64);
  assert(model.providers[0].resets==-1&&model.providers[1].resets==2);
  for(size_t length=0;length<raw.size();++length) {
    std::string truncated=raw.substr(0,length);
    auto output=model;
    assert(!meter::decode(truncated.c_str(),truncated.size(),output));
    assert(output.providers[1].weekly.remaining==64);
    for(char replacement:{'\\','"','[','}', '\0',static_cast<char>(0xff)}) {
      auto mutated=raw;mutated[length]=replacement;
      meter::decode(mutated.c_str(),mutated.size(),output);
    }
  }
  for(const auto &mutation:std::vector<std::pair<std::string,std::string>>{
    {"\"version\":3","\"version\":3,\"version\":3"},
    {"\"version\":3","\"version\":2"},
    {"\"remaining_percent\":72","\"remaining_percent\":101"},
    {"\"remaining_percent\":72","\"remaining_percent\":true"},
    {"\"remaining_percent\":72","\"remaining_percent\":-1"},
    {"\"remaining_percent\":72","\"remaining_percent\":1e999"},
    {"\"source_error\":\"\"","\"source_error\":\"not_valid\""},
    {"\"observed_at\":1800000000","\"observed_at\":0"},
    {"\"resets_available\":2","\"resets_available\":-1"},
    {"\"resets_available\":2","\"resets_available\":2147483648"},
    {"\"resets_available\":2","\"resets_available\":1.5"},
    {"\"resets_available\":2","\"resets_available\":\"2\""},
    {"\"resets_available\":2","\"resets_available\":true"},
    {"\"resets_available\":2","\"resets_available\":2,\"resets_available\":2"},
    {"\"reset_at\":1800008040","\"reset_at\":-1"}}) {
    std::string changed=raw;const auto i=changed.find(mutation.first);assert(i!=std::string::npos);
    changed.replace(i,mutation.first.size(),mutation.second);
    meter::Model output;assert(!meter::decode(changed.c_str(),changed.size(),output));
  }
  meter::State state;assert(state.accept(model,0));assert(!state.stale(0,0));
  assert(state.stale(0,900000));
  const auto old_clock=state.as_of(900000);
  assert(state.accept(model,900000));assert(state.as_of(900000)>=old_clock&&state.stale(0,900000));
  auto next=model;next.updated+=60;next.as_of+=60;
  next.providers[0].observed+=60;
  next.providers[0].five_hour.remaining=60;
  std::strcpy(next.providers[1].error,"auth_failed");
  assert(state.accept(next,901000));
  assert(state.model.providers[0].five_hour.remaining==60&&state.model.providers[1].weekly.remaining==64);
  auto absent=next;absent.updated++;absent.providers[0]=meter::Provider{};
  assert(state.accept(absent,902000)&&state.model.providers[0].five_hour.remaining==60);
  assert(state.stale(0,902000));
  assert(!state.accept(model,903000)); // Old publications cannot roll back the cache.
  auto legacy=raw;
  for(const std::string field:{",\"resets_available\":null",",\"resets_available\":2"}) {
    const auto index=legacy.find(field);assert(index!=std::string::npos);legacy.erase(index,field.size());
  }
  meter::Model old;assert(meter::decode(legacy.c_str(),legacy.size(),old)&&old.providers[1].resets==-1);
  meter::State repeated;assert(repeated.accept(model,0));
  auto changed=model;changed.providers[1].resets=3;
  assert(repeated.accept(changed,0)&&repeated.model.providers[1].resets==2&&repeated.stale(1,0));
}

void test_resets(const meter::Model &model) {
  meter::State state;assert(state.accept(model,0));meter::settings::Controller settings;
  auto frame=meter::ui::project(state,0,settings);
  assert(!frame.providers[0].resets[0]&&meter::eq(frame.providers[1].resets,"2"));
  auto stale=meter::ui::project(state,900000,settings);assert(!stale.providers[1].resets[0]);
  state.model.providers[1].resets=0;
  auto zero=meter::ui::project(state,0,settings);assert(!zero.providers[1].resets[0]&&!meter::ui::equal(frame,zero));
  for(int pos=0;pos<4;++pos)for(const char *count:{"", "1", "10", "2147483647"}) {
    Canvas canvas(pos);const int left=24+meter::ui::text_width("CLAUDE",pos&1?meter::font::provider:meter::font::title);
    meter::ui::reset_counter(canvas,count,left,canvas.width-12,50,meter::ui::CLAUDE);
    bool painted=false;
    for(int y=0;y<canvas.height;++y)for(int x=0;x<canvas.width;++x) {
      const size_t index=(y*canvas.width+x)*3;
      if(canvas.pixels[index]||canvas.pixels[index+1]||canvas.pixels[index+2]) {
        assert(x>=left&&x<canvas.width-12&&y>=50&&y<64);painted=true;
      }
    }
    assert(painted==bool(count[0]));
  }
}

void test_quota_size() {
  const int narrow_width=(meter::orientation::width(1)-40)/2;
  for(int value=0;value<=100;++value) {
    const auto limit=meter::ui::project_limit({true,static_cast<double>(value),0},0);
    assert(meter::ui::text_width(limit.value,meter::font::quota)<=narrow_width);
  }
  for(const char *value:{"0%","1%","62%","99%","100%","<1%",">99%","--%"}) {
    assert(meter::ui::text_width(value,meter::font::quota)<=narrow_width);
    for(int width:{narrow_width,meter::orientation::width(1)-24}) {
      meter::ui::Limit limit;limit.known=!meter::eq(value,"--%");std::strcpy(limit.value,value);
      Canvas actual(1),expected(1);
      meter::ui::draw_limit(actual,limit,"WEEKLY",12,80,width,meter::ui::CODEX,false);
      meter::ui::text(expected,12,99,value,meter::font::quota,limit.known?meter::ui::INK:meter::ui::MUTED);
      // Narrow and full-width cards must render the same glyphs for every percentage.
      for(int y=99;y<99+meter::font::quota.height;++y)for(int x=0;x<actual.width*3;++x) {
        assert(actual.pixels[y*actual.width*3+x]==expected.pixels[y*actual.width*3+x]);
        if(actual.pixels[y*actual.width*3+x])assert(x/3>=12&&x/3<12+width);
      }
    }
  }
}

int main(int argc,char **argv) {
  assert(argc==3);
  std::ifstream in(argv[1]);std::string raw((std::istreambuf_iterator<char>(in)),{});
  while(!raw.empty() && (raw.back()=='\n' || raw.back()==' ' || raw.back()=='\r'))raw.pop_back();
  meter::Model model;test_model(raw,model);test_settings();test_resets(model);test_quota_size();
  const std::filesystem::path output=argv[2];std::filesystem::create_directories(output);
  meter::State state;assert(state.accept(model,0));
  auto sample=state;
  sample.model.providers[0].five_hour={true,100,0};sample.model.providers[0].weekly.remaining=62;
  sample.model.providers[1].five_hour={};sample.model.providers[1].weekly.remaining=94;
  meter::settings::Controller preview_settings;
  auto preview=meter::ui::project(sample,0,preview_settings);preview.battery=100;
  Canvas preview_canvas(1);meter::ui::draw(preview_canvas,preview,1);preview_canvas.save(output/"full-percentage.ppm");
  int rendered=0;
  for(int pos=0;pos<4;++pos)for(int flags=0;flags<16;++flags)for(bool menu:{false,true}) {
    meter::settings::Controller settings;settings.values.position=pos;settings.values.flags=flags;settings.menu=menu;
    for(int scenario=0;scenario<10;++scenario) {
      auto sample=state;
      if(scenario==1)sample.model.providers[1].five_hour={};
      if(scenario==2)sample.model.providers[0]=meter::Provider{};
      if(scenario==3)sample.transport_failed=true;
      if(scenario==4)for(auto &p:sample.model.providers){p.five_hour.remaining=100;p.weekly.remaining=0;}
      if(scenario==5)for(auto &p:sample.model.providers){p.five_hour.remaining=0.3;p.weekly.remaining=99.9;}
      if(scenario==6){settings.save_failed=true;std::strcpy(sample.model.providers[0].error,"auth_missing");sample.model.providers[0].observed=0;}
      if(scenario==7)for(auto &p:sample.model.providers)p.resets=0;
      if(scenario==8)for(auto &p:sample.model.providers)p.resets=2147483647;
      if(scenario==9)sample.model.providers[0].resets=1;
      auto frame=meter::ui::project(sample,scenario==3?604800000:0,settings);frame.battery=scenario==4?100:72;
      Canvas full(pos);meter::ui::draw(full,frame,pos);
      Canvas paged(pos);
      for(int page=0;page<4;++page){paged.clip_start=page*paged.height/4;paged.clip_end=(page+1)*paged.height/4;meter::ui::draw(paged,frame,pos);}
      assert(full.pixels==paged.pixels);
      if(flags==15&&scenario==0)full.save(output/(std::string(menu?"settings-":"usage-")+std::to_string(pos)+".ppm"));
      if(pos==1&&!menu&&flags==15&&scenario==1)full.save(output/"weekly-only.ppm");
      if(pos==1&&!menu&&flags==15&&scenario==3)full.save(output/"stale.ppm");
      if(pos==1&&flags==15&&scenario==6)full.save(output/(menu?"save-failed.ppm":"auth-missing.ppm"));
      if(pos==1&&!menu&&flags==0&&scenario==0)full.save(output/"disabled.ppm");
      if(pos==1&&!menu&&flags==15&&scenario>=7)full.save(output/(std::string("resets-")+std::to_string(scenario)+".ppm"));
      ++rendered;
    }
  }
  std::cout<<"PASS: "<<rendered<<" bounded renders, paged-frame equivalence, settings/restart/touch, malformed payloads and stale-data recovery. Frame bytes="<<sizeof(meter::ui::Frame)<<"\n";
}
