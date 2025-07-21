// file: internal/core/port/workflow.go

package port

// PluginExecutorProvider 定义了一个能够根据业务名提供插件执行器的对象的行为。
// 任何实现了这个接口的类型，都可以被工作流服务使用。
type PluginExecutorProvider interface {
	GetExecutor(bizName string) (Executor, bool)
}
