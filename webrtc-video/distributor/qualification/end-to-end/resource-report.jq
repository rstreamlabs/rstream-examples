def bytes:
  capture("^(?<value>[0-9]+(?:\\.[0-9]+)?)(?<unit>[A-Za-z]+)$") as $measurement |
  ($measurement.value | tonumber) * (
    {
      B: 1,
      kB: 1000,
      MB: 1000000,
      GB: 1000000000,
      TB: 1000000000000,
      KiB: 1024,
      MiB: 1048576,
      GiB: 1073741824,
      TiB: 1099511627776
    }[$measurement.unit] // error("unsupported byte unit: \($measurement.unit)")
  );

def percentile(values; percentile):
  (values | sort) as $sorted |
  $sorted[((($sorted | length) * percentile | ceil) - 1)];

def range(values):
  {
    average: ((values | add) / (values | length)),
    p95: percentile(values; 0.95),
    maximum: (values | max)
  };

def role(name):
  if name == $producer_name then "producer"
  elif name == $browser_name then "browser"
  elif $distributor_name != "" and name == $distributor_name then "distributor"
  else null
  end;

def resident_bytes:
  if (.ResidentBytes? | type) == "number" then .ResidentBytes
  else (.MemUsage | split(" / ")[0] | bytes)
  end;

def resident_bytes_source:
  .ResidentBytesSource // "container-cgroup";

def component(samples):
  samples as $samples |
  {
    samples: ($samples | length),
    cpuCoreRatio: range([$samples[] | .CPUPerc | rtrimstr("%") | tonumber / 100]),
    residentBytes: (
      range([$samples[] | resident_bytes]) +
      {sources: ([$samples[] | resident_bytes_source] | unique)}
    ),
    tasks: {maximum: ([$samples[] | .PIDs | tonumber] | max)},
    network: {
      receivedBytes: ([$samples[] | .NetIO | split(" / ")[0] | bytes] | max),
      transmittedBytes: ([$samples[] | .NetIO | split(" / ")[1] | bytes] | max)
    }
  };

map(. + {role: role(.Name)}) |
map(select(.role != null)) as $samples |
($samples | group_by(.role) | map({key: .[0].role, value: component(.)}) | from_entries) as $components |
{
  components: $components,
  conservativePeak: {
    cpuCoreRatio: ([$components[].cpuCoreRatio.maximum] | add),
    residentBytes: ([$components[].residentBytes.maximum] | add),
    tasks: ([$components[].tasks.maximum] | add)
  }
}
