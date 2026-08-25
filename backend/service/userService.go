package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
	"iot-gateway/utils"
)

// userService 用户服务实现
type UserService struct {
	sqliteDB *gorm.DB
	//redisCache *cache.RedisCache
}

// NewUserService 创建用户服务（依赖通过参数注入）
func NewUserService(sqliteDB *gorm.DB) *UserService {
	return &UserService{
		sqliteDB: sqliteDB,
		//redisCache: redisCache,
	}
}

// CreateUser 创建用户
func (s *UserService) CreateUser(ctx context.Context, req *dto.CreateUserDTO) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查邮箱是否已存在
		var existing po.User
		if err := tx.Where("username = ?", req.Username).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if existing.ID != "" {
			return appError.NewAppErrorCtx(enums.UserExistsEnum.GetCode(), enums.UserExistsEnum.GetMessage(), enums.UserExistsEnum.GetMsgKey())
		}

		// 密码哈希
		hashedPassword, err := utils.HashPassword(req.Password)
		if err != nil {
			return err
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		// 创建用户
		user := &po.User{
			ID:        utils.GenerateUUID(),
			Username:  req.Username,
			Password:  hashedPassword,
			Status:    1,
			CreatedAt: now,
			UpdatedAt: now,
		}

		if err := tx.Create(user).Error; err != nil {
			return err
		}
		return nil
	})
	return err
}

// GetUser 获取用户
func (s *UserService) GetUser(ctx context.Context, id string) (*vo.UserVO, error) {
	var user po.User
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.UserNotExistsEnum.GetCode(), enums.UserNotExistsEnum.GetMessage(), enums.UserNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}

	return &vo.UserVO{
		ID:        user.ID,
		Name:      user.Username,
		Email:     user.Email,
		Avatar:    user.Avatar,
		Status:    user.Status,
		CreatedAt: user.CreatedAt,
	}, nil
}

// UpdateUser 更新用户
func (s *UserService) UpdateUser(ctx context.Context, id string, req *dto.UpdateUserDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := make(map[string]interface{})
		if req.Name != "" {
			updates["username"] = req.Name
		}
		if req.Email != "" {
			updates["email"] = req.Email
		}
		if req.Avatar != "" {
			updates["avatar"] = req.Avatar
		}
		if len(updates) == 0 {
			return nil
		}
		return tx.Model(&po.User{}).Where("id = ?", id).Updates(updates).Error
	})
}

// Login 用户登录
func (s *UserService) Login(ctx context.Context, req *dto.LoginDTO) (*vo.LoginVO, error) {
	username, err := utils.RSADecrypt(req.Username, utils.PrivateKey)
	if err != nil {
		return nil, err
	}
	password, err := utils.RSADecrypt(req.Password, utils.PrivateKey)
	if err != nil {
		return nil, err
	}

	var user po.User
	if err := s.sqliteDB.WithContext(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(
				enums.UserNotExistsEnum.GetCode(),
				enums.UserNotExistsEnum.GetMessage(),
				enums.UserNotExistsEnum.GetMsgKey(),
			)
		}
		return nil, err
	}

	if !utils.CheckPassword(password, user.Password) {
		return nil, appError.NewAppErrorCtx(
			enums.PasswordErrEnum.GetCode(),
			enums.PasswordErrEnum.GetMessage(),
			enums.PasswordErrEnum.GetMsgKey(),
		)
	}

	// 生成令牌
	token, err := utils.GenerateToken(user.ID)
	if err != nil {
		return nil, err
	}

	return &vo.LoginVO{
		AccessToken: token,
	}, nil
}

// PageUser 用户分页查询
func (s *UserService) PageUser(ctx context.Context, pageDTO *dto.PageDTO) (*response.PageVo[vo.UserVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.User{})

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var users []po.User
	offset := (pageDTO.Page - 1) * pageDTO.Size
	if err := db.Offset(offset).Limit(pageDTO.Size).Find(&users).Error; err != nil {
		return nil, err
	}

	records := make([]vo.UserVO, len(users))
	for i, u := range users {
		records[i] = vo.UserVO{
			ID:        u.ID,
			Name:      u.Username,
			Email:     u.Email,
			Avatar:    u.Avatar,
			Status:    u.Status,
			CreatedAt: u.CreatedAt,
		}
	}

	return &response.PageVo[vo.UserVO]{
		Page:    pageDTO.Page,
		Size:    pageDTO.Size,
		Total:   total,
		Records: records,
	}, nil
}
