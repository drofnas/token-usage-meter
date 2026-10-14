#pragma once
#include "model.h"
#include "settings.h"
#include "fonts.h"
#include <cstdio>

namespace meter::ui {
constexpr uint32_t BG=0x101820,INK=0xF3F6F7,MUTED=0xAAB8C2,TRACK=0x324450;
constexpr uint32_t CLAUDE=0xD99A78,CODEX=0x63DFC1,WARN=0xFFC269;
struct Limit {
  bool known=false,due=false;
  uint8_t fill=0;
  char value[6]="--%",reset[20]="Waiting for usage";
};
struct ProviderFrame {
  Limit five,weekly;
  bool stale=false;
  char message[24]{};
  char resets[11]{};
};
struct Frame {
  ProviderFrame providers[2];
  settings::Values settings;
  bool menu=false,save_failed=false;
  int8_t battery=-2;
};
inline Limit project_limit(const Window &w,int64_t now) {
  Limit f;f.known=w.known;
  if(!w.known)return f;
  f.fill=static_cast<uint8_t>(std::lround(w.remaining));
  if(w.remaining>0&&w.remaining<1)std::strcpy(f.value,"<1%");
  else if(w.remaining>99&&w.remaining<100)std::strcpy(f.value,">99%");
  else std::snprintf(f.value,sizeof(f.value),"%d%%",f.fill);
  if(!w.reset)std::strcpy(f.reset,"Not started");
  else if(now>=w.reset) { f.due=true;std::strcpy(f.reset,"Reset pending"); }
  else {
    const auto seconds=w.reset-now;
    if(seconds>=DAY)std::snprintf(f.reset,sizeof(f.reset),"Reset %lldd %lldh",static_cast<long long>(std::min<int64_t>(999,seconds/DAY)),static_cast<long long>((seconds%DAY)/3600));
    else if(seconds>=3600)std::snprintf(f.reset,sizeof(f.reset),"Reset %lldh %lldm",static_cast<long long>(seconds/3600),static_cast<long long>((seconds%3600)/60));
    else if(seconds>=60)std::snprintf(f.reset,sizeof(f.reset),"Reset %lldm",static_cast<long long>(seconds/60));
    else std::strcpy(f.reset,"Reset <1m");
  }
  return f;
}
inline Frame project(const State &state,uint64_t now,const settings::Controller &controller) {
  Frame f;f.settings=controller.values;f.menu=controller.menu;f.save_failed=controller.save_failed;
  for(size_t i=0;i<2;++i) {
    auto &p=f.providers[i];const auto &source=state.model.providers[i];
    p.five=project_limit(source.five_hour,state.as_of(now));
    p.weekly=project_limit(source.weekly,state.as_of(now));
    p.stale=state.stale(i,now);
    if(source.observed&&!p.stale&&source.resets>0)std::snprintf(p.resets,sizeof(p.resets),"%lld",static_cast<long long>(source.resets));
    if(!source.observed)std::strcpy(p.message,eq(source.error,"auth_missing")||eq(source.error,"auth_failed")?"Sign in on your Mac":"Waiting for usage");
    else if(!p.five.known&&!p.weekly.known)std::strcpy(p.message,"No limits available");
    else if(p.stale)std::strcpy(p.message,"Last known");
  }
  return f;
}
inline bool equal_limit(const Limit &a,const Limit &b) {
  return a.known==b.known&&a.due==b.due&&a.fill==b.fill&&eq(a.value,b.value)&&eq(a.reset,b.reset);
}
inline bool equal(const Frame &a,const Frame &b) {
  if(a.battery!=b.battery||a.menu!=b.menu||a.save_failed!=b.save_failed||a.settings.encode()!=b.settings.encode())return false;
  for(int i=0;i<2;++i) {
    const auto &x=a.providers[i],&y=b.providers[i];
    if(x.stale!=y.stale||!eq(x.message,y.message)||!eq(x.resets,y.resets)||!equal_limit(x.five,y.five)||!equal_limit(x.weekly,y.weekly))return false;
  }
  return true;
}
inline int text_width(const char *s,const font::Font &f) {
  int width=0;
  for(;*s;++s)width+=f.glyphs[static_cast<unsigned char>(*s)>=32&&static_cast<unsigned char>(*s)<=127?static_cast<unsigned char>(*s)-32:'?'-32].advance;
  return width;
}
inline uint32_t blend(uint32_t foreground,uint32_t background,int alpha) {
  uint32_t color=0;
  for(int shift:{0,8,16})color|=(((foreground>>shift&255)*alpha+(background>>shift&255)*(3-alpha))/3)<<shift;
  return color;
}
template<class Canvas> void text(Canvas &c,int x,int y,const char *s,const font::Font &f,uint32_t color,uint32_t background=BG) {
  for(;*s;++s) {
    const unsigned char code=*s;
    const auto &g=f.glyphs[code>=32&&code<=127?code-32:'?'-32];
    for(int row=0;row<g.height;++row)for(int col=0;col<g.width;) {
      const int index=row*g.width+col;
      const int level=(f.data[g.offset+index/4]>>((index%4)*2))&3;
      int end=col+1;
      while(end<g.width) {
        const int next=row*g.width+end;
        if(((f.data[g.offset+next/4]>>((next%4)*2))&3)!=level)break;
        ++end;
      }
      if(level)c.rect(x+g.left+col,y+g.top+row,end-col,1,blend(color,background,level));
      col=end;
    }
    x+=g.advance;
  }
}
template<class Canvas> void rounded(Canvas &c,int x,int y,int w,int h,int radius,uint32_t color) {
  if(w<=0||h<=0)return;
  radius=std::min(radius,std::min(w,h)/2);
  for(int row=0;row<h;++row) {
    const int dy=row<radius?radius-row-1:row>=h-radius?row-(h-radius):0;
    const int inset=dy?radius-static_cast<int>(std::sqrt(radius*radius-dy*dy)):0;
    c.rect(x+inset,y+row,w-2*inset,1,color);
  }
}
template<class Canvas> void gear(Canvas &c,int x,int y) {
  for(int yy=-11;yy<=11;++yy)for(int xx=-11;xx<=11;++xx) {
    const int d=xx*xx+yy*yy;
    const bool tooth=(std::abs(xx)<=3||std::abs(yy)<=3||std::abs(std::abs(xx)-std::abs(yy))<=2);
    if(d>=16&&(d<=70||(d<=121&&tooth)))c.rect(x+xx,y+yy,1,1,INK);
  }
}
template<class Canvas> void reset_counter(Canvas &c,const char *count,int left,int right,int y,uint32_t color) {
  if(!count[0])return;
  const auto &f=18+text_width(count,font::title)<=right-left?font::title:font::small;
  const int x=right-18-text_width(count,f);
  static constexpr uint8_t reload[7]={0x1C,0x22,0x41,0x41,0x45,0x26,0x16};
  for(int col=0;col<7;++col)for(int row=0;row<7;++row)
    if(reload[col]&(1<<row))c.rect(x+col*2,y+row*2,2,2,color);
  const auto &digit=f.glyphs['0'-32];
  text(c,x+18,y+(14-digit.height)/2-digit.top,count,f,color);
}
template<class Canvas> void battery(Canvas &c,const Frame &f,int x) {
  if(f.battery==-2) { text(c,x,15,"USB",font::label,INK);return; }
  c.rect(x,13,16,11,INK);c.rect(x+16,16,2,5,INK);c.rect(x+2,15,12,7,BG);
  if(f.battery>=0)c.rect(x+3,16,std::max(0,10*f.battery/100),5,INK);
  char value[6]="--%";if(f.battery>=0)std::snprintf(value,sizeof(value),"%d%%",f.battery);
  text(c,x+21,15,value,font::label,INK);
}
template<class Canvas> void header(Canvas &c,const Frame &f,int width) {
  if(f.menu) {
    for(int i=0;i<9;++i) { c.rect(13+i,19-i,3,3,INK);c.rect(13+i,19+i,3,3,INK); }
    text(c,40,13,"SETTINGS",font::title,INK);battery(c,f,width-71);
  } else {
    text(c,12,13,"USAGE",font::title,INK);
    text(c,78,18,"REMAINING",font::small,MUTED);
    battery(c,f,width-95);gear(c,width-21,20);
  }
}
template<class Canvas> void toggle(Canvas &c,const orientation::Rect &r,bool on,bool enabled,uint32_t accent) {
  const int x=r.x+r.width-48,y=r.y+(r.height-24)/2;
  rounded(c,x,y,44,24,12,on&&enabled?accent:TRACK);
  rounded(c,x+(on?23:3),y+3,18,18,9,enabled?INK:MUTED);
}
template<class Canvas> void draw_settings(Canvas &c,const Frame &f,int width) {
  const bool portrait=f.settings.position&1;
  const settings::Action toggles[]={settings::Action::claude,settings::Action::claude_five,settings::Action::codex,settings::Action::codex_five};
  for(int i=0;i<4;++i) {
    const auto r=settings::control(toggles[i],f.settings.position).rect;
    const int p=i/2;const uint32_t accent=p?CODEX:CLAUDE;
    const bool enabled=f.settings.enabled(p);
    text(c,i%2?24:12,r.y+(i%2?11:9),i%2?"Show 5-hour limit":p?"CODEX":"CLAUDE",i%2?font::body:font::provider,i%2?(enabled?INK:MUTED):accent);
    toggle(c,r,i%2?f.settings.five_hour(p):enabled,i%2?enabled:true,accent);
  }
  c.rect(12,portrait?140:109,width-24,1,TRACK);
  c.rect(12,portrait?238:187,width-24,1,TRACK);
  text(c,12,portrait?251:207,"ROTATION",font::label,INK);
  for(int i=0;i<4;++i) {
    const auto action=static_cast<settings::Action>(static_cast<int>(settings::Action::rotate0)+i);
    const auto r=settings::control(action,f.settings.position).rect;
    const bool selected=f.settings.position==i;
    const uint32_t fill=selected?CODEX:TRACK;
    rounded(c,r.x,r.y,r.width,r.height,4,fill);
    char label[6];std::snprintf(label,sizeof(label),"%d\x7f",i*90);
    text(c,r.x+(r.width-text_width(label,font::title))/2,r.y+(r.height-13)/2,label,font::title,selected?BG:INK,fill);
  }
  if(f.save_failed)text(c,portrait?12:150,portrait?40:16,"Settings not saved",font::small,WARN);
}
template<class Canvas> void draw_limit(Canvas &c,const Limit &f,const char *label,int x,int y,int width,uint32_t accent,bool stale) {
  const uint32_t color=stale||f.due?WARN:accent;
  text(c,x,y,label,font::label,INK);
  text(c,x,y+19,f.value,font::quota,f.known?INK:MUTED);
  rounded(c,x,y+56,width,6,2,TRACK);
  if(f.known&&f.fill)rounded(c,x,y+56,std::max(1,width*f.fill/100),6,2,color);
  text(c,x,y+72,f.reset,font::small,f.due?WARN:MUTED);
}
template<class Canvas> void draw(Canvas &c,const Frame &f,uint8_t position) {
  const int width=orientation::width(position),height=orientation::height(position);
  c.rect(0,0,width,height,BG);header(c,f,width);
  if(f.menu) { draw_settings(c,f,width);return; }
  const int count=static_cast<int>(f.settings.enabled(0))+static_cast<int>(f.settings.enabled(1));
  if(!count) {
    text(c,12,height/2-18,"No providers enabled",font::body,INK);
    text(c,12,height/2+5,"Open settings to choose one",font::small,MUTED);return;
  }
  const bool portrait=position&1;
  int slot=0;
  for(int i=0;i<2;++i) {
    if(!f.settings.enabled(i))continue;
    const int top=portrait?(count==1?90:52+slot*139):42+slot*96;
    const auto &p=f.providers[i];const uint32_t accent=i?CODEX:CLAUDE;
    const char *title=i?"CODEX":"CLAUDE";
    const auto &title_font=portrait?font::provider:font::title;
    text(c,12,top,title,title_font,accent);
    if(p.message[0])text(c,width-12-text_width(p.message,font::small),top+5,p.message,font::small,p.stale?WARN:MUTED);
    else reset_counter(c,p.resets,24+text_width(title,title_font),width-12,top+(portrait?4:0),accent);
    const int y=top+(portrait?29:23);
    const bool show_five=f.settings.five_hour(i)&&p.five.known;
    const bool show_weekly=p.weekly.known||!show_five;
    const int col_width=show_five&&show_weekly?(width-40)/2:width-24;
    if(portrait) {
      if(show_five)draw_limit(c,p.five,"5HR LIMIT",12,y,col_width,accent,p.stale);
      if(show_weekly)draw_limit(c,p.weekly,"WEEKLY",show_five?28+col_width:12,y,col_width,accent,p.stale);
    } else {
      for(int j=0;j<(show_five&&show_weekly?2:1);++j) {
        const bool five=show_five&&j==0;const auto &limit=five?p.five:p.weekly;
        const int x=12+j*(col_width+16);
        text(c,x,y,five?"5HR LIMIT":"WEEKLY",font::small,INK);
        text(c,x+col_width-text_width(limit.value,font::provider),y-4,limit.value,font::provider,limit.known?INK:MUTED);
        rounded(c,x,y+25,col_width,5,2,TRACK);
        if(limit.known&&limit.fill)rounded(c,x,y+25,std::max(1,col_width*limit.fill/100),5,2,p.stale||limit.due?WARN:accent);
        text(c,x,y+39,limit.reset,font::small,limit.due?WARN:MUTED);
      }
    }
    if(++slot<count)c.rect(12,portrait?174:133,width-24,1,TRACK);
  }
}
} // namespace meter::ui
