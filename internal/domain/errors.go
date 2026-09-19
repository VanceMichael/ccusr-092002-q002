package domain

import "errors"

// 领域规则错误，HTTP 层据此映射状态码。
var (
	// ErrNotFound 记录不存在或对当前经办人不可见。
	ErrNotFound = errors.New("记录不存在")
	// ErrParkScope 经办人只能处理所属园区的材料。
	ErrParkScope = errors.New("无权处理非所属园区的材料")
	// ErrCrossPark 材料归属其他园区，须先发起跨园转交并取得接收回执。
	ErrCrossPark = errors.New("材料归属其他园区，须走跨园转交")
	// ErrAlreadyExists 唯一标识冲突，例如同一授权编号重复登记。
	ErrAlreadyExists = errors.New("记录已存在")
	// ErrPublished 意向已对外公开，合作范围不可再调整。
	ErrPublished = errors.New("意向已公开，不能再调整合作范围")
	// ErrFinalized 意向已签约或关闭，不可再变更。
	ErrFinalized = errors.New("意向已终态")
	// ErrNoAuthorization 尚未取得企业授权。
	ErrNoAuthorization = errors.New("尚未取得企业授权")
	// ErrNoPolicy 尚无可用的优惠政策版本。
	ErrNoPolicy = errors.New("尚无可用政策版本")
	// ErrPolicyChanged 签约前政策版本已变化，须企业重新确认。
	ErrPolicyChanged = errors.New("政策版本已变化，须重新确认")
	// ErrTransferState 转交单当前状态不允许该操作。
	ErrTransferState = errors.New("转交单状态不允许该操作")
	// ErrInvalidInput 输入不满足领域要求。
	ErrInvalidInput = errors.New("输入无效")
	// ErrDuplicateSource 同一来源记录已接收过。
	ErrDuplicateSource = errors.New("来源记录已接收")
)
