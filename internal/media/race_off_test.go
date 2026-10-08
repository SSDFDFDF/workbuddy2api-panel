//go:build !race

package media

// raceEnabled 报告当前二进制是否由 -race 构建。竞态运行时的插桩自身会产生分配，
// 使 AllocsPerRun 的"精确 0 分配"断言失去意义——热路径分配测试据此跳过精确断言。
const raceEnabled = false
