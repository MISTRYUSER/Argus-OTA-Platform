package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
)

// MockBatchRepository - BatchRepository 的 Mock 实现
type MockBatchRepository struct {
	mock.Mock
}

func (m *MockBatchRepository) Save(ctx context.Context, batch *domain.Batch) error {
	args := m.Called(ctx, batch)
	return args.Error(0)
}

func (m *MockBatchRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Batch, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Batch), args.Error(1)
}

func (m *MockBatchRepository) FindByVIN(ctx context.Context, vin string) ([]*domain.Batch, error) {
	args := m.Called(ctx, vin)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Batch), args.Error(1)
}

func (m *MockBatchRepository) FindByStatus(ctx context.Context, status domain.BatchStatus) ([]*domain.Batch, error) {
	args := m.Called(ctx, status)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Batch), args.Error(1)
}

func (m *MockBatchRepository) List(ctx context.Context, opts domain.ListOptions) ([]*domain.Batch, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Batch), args.Error(1)
}

func (m *MockBatchRepository) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

// MockKafkaEventPublisher - KafkaEventPublisher 的 Mock 实现
type MockKafkaEventPublisher struct {
	mock.Mock
}

func (m *MockKafkaEventPublisher) PublishEvents(ctx context.Context, events []domain.DomainEvent) error {
	args := m.Called(ctx, events)
	return args.Error(0)
}

func (m *MockKafkaEventPublisher) Close() error {
	args := m.Called()
	return args.Error(0)
}

// TestCreateBatch_Success - 测试成功创建 Batch
func TestCreateBatch_Success(t *testing.T) {
	// 1. 创建 Mock
	mockRepo := new(MockBatchRepository)
	mockKafka := new(MockKafkaEventPublisher)

	// 2. 设置期望（Save 会被调用两次，PublishEvents 会被调用一次）
	mockRepo.On("Save", mock.Anything, mock.AnythingOfType("*domain.Batch")).Return(nil).Times(2)
	mockKafka.On("PublishEvents", mock.Anything, mock.AnythingOfType("[]domain.DomainEvent")).Return(nil)

	// 3. 创建 BatchService
	service := application.NewBatchService(mockRepo, mockKafka)

	// 4. 执行测试
	ctx := context.Background()
	batch, err := service.CreateBatch(ctx, "vehicle-001", "VIN123", 5)

	// 5. 验证结果
	assert.NoError(t, err)
	assert.NotNil(t, batch)
	assert.Equal(t, "vehicle-001", batch.VehicleID)
	assert.Equal(t, "VIN123", batch.VIN)
	assert.Equal(t, 5, batch.ExpectedWorkerCount)
	assert.Equal(t, domain.BatchStatusPending, batch.Status)

	// 6. 验证 Mock 调用
	mockRepo.AssertExpectations(t)
	mockKafka.AssertExpectations(t)
}

// TestCreateBatch_RepositoryError - 测试 Repository 保存失败
func TestCreateBatch_RepositoryError(t *testing.T) {
	// 1. 创建 Mock
	mockRepo := new(MockBatchRepository)
	mockKafka := new(MockKafkaEventPublisher)

	// 2. 设置期望：第一次 Save 就失败
	mockRepo.On("Save", mock.Anything, mock.AnythingOfType("*domain.Batch")).Return(errors.New("database error"))

	// 3. 创建 BatchService
	service := application.NewBatchService(mockRepo, mockKafka)

	// 4. 执行测试
	ctx := context.Background()
	batch, err := service.CreateBatch(ctx, "vehicle-001", "VIN123", 5)

	// 5. 验证结果
	assert.Error(t, err)
	assert.Nil(t, batch)
	assert.Contains(t, err.Error(), "database error")

	// 6. 验证 Mock 调用
	mockRepo.AssertExpectations(t)
}

// TestTransitionBatchStatus_Success - 测试成功转换状态
func TestTransitionBatchStatus_Success(t *testing.T) {
	// 1. 创建测试数据
	testBatch, _ := domain.NewBatch("vehicle-001", "VIN123", 5)
	testBatch.TransitionTo(domain.BatchStatusUploaded) // 先转换到 uploaded

	// 2. 创建 Mock
	mockRepo := new(MockBatchRepository)
	mockKafka := new(MockKafkaEventPublisher)

	// 3. 设置期望
	mockRepo.On("FindByID", mock.Anything, testBatch.ID).Return(testBatch, nil)
	mockRepo.On("Save", mock.Anything, mock.AnythingOfType("*domain.Batch")).Return(nil)
	mockKafka.On("PublishEvents", mock.Anything, mock.AnythingOfType("[]domain.DomainEvent")).Return(nil)

	// 4. 创建 BatchService
	service := application.NewBatchService(mockRepo, mockKafka)

	// 5. 执行测试
	ctx := context.Background()
	err := service.TransitionBatchStatus(ctx, testBatch.ID, domain.BatchStatusScattering)

	// 6. 验证结果
	assert.NoError(t, err)

	// 7. 验证 Mock 调用
	mockRepo.AssertExpectations(t)
	mockKafka.AssertExpectations(t)
}

// TestTransitionBatchStatus_BatchNotFound - 测试 Batch 不存在
func TestTransitionBatchStatus_BatchNotFound(t *testing.T) {
	// 1. 创建测试数据
	batchID := uuid.New()

	// 2. 创建 Mock
	mockRepo := new(MockBatchRepository)
	mockKafka := new(MockKafkaEventPublisher)

	// 3. 设置期望：返回 nil（Batch 不存在）
	mockRepo.On("FindByID", mock.Anything, batchID).Return(nil, nil)

	// 4. 创建 BatchService
	service := application.NewBatchService(mockRepo, mockKafka)

	// 5. 执行测试
	ctx := context.Background()
	err := service.TransitionBatchStatus(ctx, batchID, domain.BatchStatusScattering)

	// 6. 验证结果
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "batch not found")

	// 7. 验证 Mock 调用
	mockRepo.AssertExpectations(t)
}

// TestAddFile_Success - 测试成功添加文件
func TestAddFile_Success(t *testing.T) {
	// 1. 创建测试数据
	testBatch, _ := domain.NewBatch("vehicle-001", "VIN123", 5)
	fileID := uuid.New()

	// 2. 创建 Mock
	mockRepo := new(MockBatchRepository)
	mockKafka := new(MockKafkaEventPublisher)

	// 3. 设置期望
	mockRepo.On("FindByID", mock.Anything, testBatch.ID).Return(testBatch, nil)
	mockRepo.On("Save", mock.Anything, mock.AnythingOfType("*domain.Batch")).Return(nil)

	// 4. 创建 BatchService
	service := application.NewBatchService(mockRepo, mockKafka)

	// 5. 执行测试
	ctx := context.Background()
	err := service.AddFile(ctx, testBatch.ID, fileID)

	// 6. 验证结果
	assert.NoError(t, err)
	assert.Equal(t, 1, testBatch.TotalFiles)

	// 7. 验证 Mock 调用
	mockRepo.AssertExpectations(t)
}

// TestAddFile_WrongStatus - 测试在错误状态下添加文件
func TestAddFile_WrongStatus(t *testing.T) {
	// 1. 创建测试数据
	testBatch, _ := domain.NewBatch("vehicle-001", "VIN123", 5)

	// 先转换到 uploaded，然后再到 scattering
	err := testBatch.TransitionTo(domain.BatchStatusUploaded)
	assert.NoError(t, err, "TransitionTo uploaded should succeed")

	err = testBatch.TransitionTo(domain.BatchStatusScattering)
	assert.NoError(t, err, "TransitionTo scattering should succeed")

	// 验证状态确实是 scattering
	assert.Equal(t, domain.BatchStatusScattering, testBatch.Status)

	fileID := uuid.New()

	// 2. 创建 Mock
	mockRepo := new(MockBatchRepository)
	mockKafka := new(MockKafkaEventPublisher)

	// 3. 设置期望
	mockRepo.On("FindByID", mock.Anything, testBatch.ID).Return(testBatch, nil)

	// 4. 创建 BatchService
	service := application.NewBatchService(mockRepo, mockKafka)

	// 5. 执行测试
	ctx := context.Background()
	err = service.AddFile(ctx, testBatch.ID, fileID)

	// 6. 验证结果
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not in pending or uploaded status")

	// 7. 验证 Mock 调用
	mockRepo.AssertExpectations(t)
}
