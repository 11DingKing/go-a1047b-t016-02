# Bug Reproduction

## 包的性质

当前 test_model_fix 保存的是被测模型修复后的结果源码，不是初始含 Bug 源码。要复现原始缺陷，必须检出下面固定的 parent SHA；不要在当前修复结果源码上期待重新出现修复前失败。生成系统使用的可信验证补丁和完整验证日志仅在本地留存，不提交到结果分支。

## 问题现象

冷箱故障换柜之后，故障箱的温度曲线被改写了，帮我修一下。

现象：

1. 一笔储能柜订舱装在冷箱 CN-1 里，途中按小时记了 3 条温度：5℃、6℃、7℃。
2. 冷箱故障后调 POST /api/bookings/{id}/equipment-failure，接口成功返回，备用箱也分配上了。
3. 但返回的 temp_curve 里这 3 条记录的 container_id 全变成了备用箱号，故障箱 CN-1 一条都对不上。理赔要的正是「故障箱在故障前的温度曲线」，这份材料没法举证。
4. 直接查 CN-1 的历史也一样：3 条记录还在、温度值和时间都没变，只有箱号被改写成了备用箱号。
5. 备用箱那边接过这 3 条历史是对的（业务上要求曲线连续），错的是故障箱自己那份记录不该被动。

复现：提交一笔 energy_storage 订舱，配好温控参数、装船，按顺序记 5℃、6℃、7℃ 三条温度，然后触发一次设备故障，再看接口返回的 temp_curve 和 CN-1 的历史。

期望行为：
- 换柜后故障箱自己的历史原样保留：3 条记录、温度 5/6/7、箱号仍是 CN-1；
- 设备故障记录里的 temp_curve 就是故障箱那份曲线，每条的箱号都是 CN-1；
- 备用箱拿到的是承接过去的 3 条副本，箱号是备用箱号；之后往备用箱记新温度只追加到备用箱那份历史，故障箱那份历史不受影响。

修完请保证 go test -timeout=120s -count=1 ./... 全绿。

## 含 Bug 版本

- 仓库：11DingKing/go-a1047b-t016-02
- 仓库地址：https://github.com/11DingKing/go-a1047b-t016-02.git
- parent SHA：e4e651552374d2d35320d91364e58839b852a621

## 复现步骤

```bash
git clone -- https://github.com/11DingKing/go-a1047b-t016-02.git bug-repro
cd bug-repro
git checkout --detach e4e651552374d2d35320d91364e58839b852a621
go test -timeout=120s -count=1 -run "^TestEquipmentFailure" ./internal/app/ -v
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test -timeout=120s -count=1 -run "^TestEquipmentFailure" ./internal/app/ -v
=== RUN   TestEquipmentFailureCurveKeepsTheFailedContainerID
    equipment_failure_curve_test.go:65: claim curve entry 0 container = "SP-002", want CN-1 (the failed reefer)
--- FAIL: TestEquipmentFailureCurveKeepsTheFailedContainerID (0.00s)
=== RUN   TestEquipmentFailureLeavesFailedContainerHistoryIntact
    equipment_failure_curve_test.go:86: CN-1 history entry 0 container = "SP-002", want CN-1
--- FAIL: TestEquipmentFailureLeavesFailedContainerHistoryIntact (0.00s)
=== RUN   TestEquipmentFailureCurveIsNotDisturbedByLaterReadings
    equipment_failure_curve_test.go:128: CN-1 history entry 0 container = "SP-002", want CN-1
--- FAIL: TestEquipmentFailureCurveIsNotDisturbedByLaterReadings (0.00s)
FAIL
FAIL	arcticexpress/internal/app	0.054s
FAIL

```

stderr：

```text
(empty)
```

### linux/arm64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test -timeout=120s -count=1 -run "^TestEquipmentFailure" ./internal/app/ -v
=== RUN   TestEquipmentFailureCurveKeepsTheFailedContainerID
    equipment_failure_curve_test.go:65: claim curve entry 0 container = "SP-002", want CN-1 (the failed reefer)
--- FAIL: TestEquipmentFailureCurveKeepsTheFailedContainerID (0.00s)
=== RUN   TestEquipmentFailureLeavesFailedContainerHistoryIntact
    equipment_failure_curve_test.go:86: CN-1 history entry 0 container = "SP-002", want CN-1
--- FAIL: TestEquipmentFailureLeavesFailedContainerHistoryIntact (0.00s)
=== RUN   TestEquipmentFailureCurveIsNotDisturbedByLaterReadings
    equipment_failure_curve_test.go:128: CN-1 history entry 0 container = "SP-002", want CN-1
--- FAIL: TestEquipmentFailureCurveIsNotDisturbedByLaterReadings (0.00s)
FAIL
FAIL	arcticexpress/internal/app	0.001s
FAIL

```

stderr：

```text
(empty)
```

## 通过条件

通过标准（bugfix）：
1. 定向复现命令在修复后全部通过：
   go test -timeout=120s -count=1 -run "^TestEquipmentFailure" ./internal/app/ -v
2. 全量回归通过：go test -timeout=120s -count=1 ./...（本仓库全量可跑，未做范围收敛）
3. go build ./... 与 go vet ./... 均为 exit 0
4. linux/amd64 与 linux/arm64 两个架构下上述命令均通过
5. 断言的是公开行为：设备故障返回的 temp_curve 与故障箱、备用箱两份历史的完整内容（条数 + 每条箱号 / 温度 / 阶段），不只断言长度；覆盖「故障箱历史不被改写」「备用箱承接副本」「换柜后再记新温度不污染故障箱历史」三种情形
6. 不得通过修改或跳过测试、放宽断言使结果变绿
