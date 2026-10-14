#include "firmware/components/meter_network/http.h"
#include <cassert>
#include <iostream>
#include <string>

int main(int argc, char **argv) {
  assert(argc == 6);
  meter::Http http;
  const std::string mode=argv[1];
  const bool ok=mode=="post"
    ? http.post_battery(argv[2],std::atoi(argv[3]),argv[4],std::atoi(argv[5]),"{\"version\":2}")
    : http.get(argv[2],std::atoi(argv[3]),argv[4],std::atoi(argv[5]));
  if(mode=="usage"&&ok) {
    meter::Model model;
    assert(meter::decode(http.body,http.size,model));
    assert(model.providers[0].five_hour.remaining==72&&model.providers[1].weekly.remaining==64);
    assert(model.providers[0].resets==-1&&model.providers[1].resets==2);
  }
  std::cout<<(ok?"valid":http.error)<<'\n';
}
